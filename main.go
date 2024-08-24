package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"gopkg.in/yaml.v2"
)

type Rss struct {
	XMLName xml.Name `xml:"rss"`
	Channel Channel  `xml:"channel"`
}

type Channel struct {
	Title string `xml:"title"`
	Item  []Item `xml:"item"`
}

type Item struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	Published string `xml:"pubDate"`
}

type Atom struct {
	XMLName xml.Name `xml:"feed"`
	Title   string   `xml:"title"`
	Entries []Entry  `xml:"entry"`
}

type Entry struct {
	Title     string `xml:"title"`
	Link      Link   `xml:"link"`
	Published string `xml:"published"`
}

type Link struct {
	Href string `xml:"href,attr"`
}

type Feed struct {
	URL        string `yaml:"url"`
	NtfyTopic  string `yaml:"ntfy_topic"`
	LastUpdate time.Time
}

type Config struct {
	Feeds []Feed `yaml:"feeds"`
}

func main() {
	log.SetFormatter(&log.JSONFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
	})

	var intervalFlag string
	var configFile string

	flag.StringVar(&intervalFlag, "interval", "10m", "Check interval (e.g., 30s, 20m, 2h)")
	flag.StringVar(&configFile, "config", "", "Path to config file")
	flag.Parse()

	if intervalFlag == "" || configFile == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -interval <duration> [-config <path>]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s -interval 10m -config /path/to/feeds.yaml\n", os.Args[0])
		os.Exit(1)
	}

	interval, err := time.ParseDuration(intervalFlag)
	if err != nil {
		log.Fatalf("Invalid interval format: %v", err)
	}

	log.Info("Reading config file")
	config, err := loadConfig(configFile)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}
	log.Infof("Using check interval: %v", interval)

	client := &http.Client{
		Timeout: time.Second * 30,
	}

	for {
		processFeedsAsync(config.Feeds, client)
		log.Infof("Sleeping for %v", interval)
		time.Sleep(interval)
	}
}

func processFeedsAsync(feeds []Feed, client *http.Client) {
	var wg sync.WaitGroup

	for i := range feeds {
		wg.Add(1)
		go func(feed *Feed) {
			defer wg.Done()
			processFeed(feed, client)
		}(&feeds[i])
	}

	wg.Wait()
}

func loadConfig(filename string) (*Config, error) {
	filename = expandTilde(filename)
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	now := time.Now()
	for i := range config.Feeds {
		config.Feeds[i].LastUpdate = now
	}

	return &config, nil
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

func processFeed(feed *Feed, client *http.Client) {
	logger := log.WithFields(log.Fields{"feed": feed.URL})
	logger.Infof("Checking feed")

	req, err := http.NewRequest("GET", feed.URL, nil)
	if err != nil {
		logger.Errorf("Error creating request: %v", err)
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		logger.Errorf("Error fetching feed: %v", err)
		return
	}
	defer resp.Body.Close()

	logger.Infof("Response status code: %d", resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Errorf("Error reading feed: %v", err)
		return
	}

	var rss Rss
	var atom Atom
	var isAtom bool

	if err := xml.Unmarshal(body, &rss); err != nil {
		if err := xml.Unmarshal(body, &atom); err != nil {
			logger.Errorf("Error parsing feed: %v", err)
			return
		}
		isAtom = true
	}

	if isAtom {
		logger.Infof("Processing as Atom feed")
		processAtomFeed(feed, atom, logger)
	} else {
		logger.Info("Processing as RSS feed")
		processRSSFeed(feed, rss, logger)
	}
}

func processRSSFeed(feed *Feed, rss Rss, logger *log.Entry) {
	for _, item := range rss.Channel.Item {
		published, err := parseDate(item.Published)
		if err != nil {
			logger.Errorf("Error parsing date for item in feed: %v", err)
			continue
		}

		if published.After(feed.LastUpdate) {
			feed.LastUpdate = published
			sendNotification(feed.NtfyTopic, item.Title, item.Link, logger)
		}
	}
}

func processAtomFeed(feed *Feed, atom Atom, logger *log.Entry) {
	for _, entry := range atom.Entries {
		published, err := parseDate(entry.Published)
		if err != nil {
			logger.Errorf("Error parsing date for entry in feed: %v", err)
			continue
		}

		if published.After(feed.LastUpdate) {
			feed.LastUpdate = published
			logger.Infof("Updated last published timestamp for: %s", feed.LastUpdate)
			sendNotification(feed.NtfyTopic, entry.Title, entry.Link.Href, logger)
		}
	}
}

func parseDate(dateString string) (time.Time, error) {
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822,
		time.RFC822Z,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999Z07:00",
		"Mon, 2 Jan 2006 15:04:05 -0700",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateString); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date: %s", dateString)
}

func sendNotification(topic, title, link string, logger *log.Entry) {
	message := fmt.Sprintf("%s\n\n%s", title, link)
	resp, err := http.Post(topic, "text/plain", bytes.NewBuffer([]byte(message)))
	if err != nil {
		logger.Errorf("Error sending notification: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Errorf("Failed to send notification: %s", resp.Status)
	} else {
		logger.Infof("Notification sent:\n\n%s", message)
	}
}
