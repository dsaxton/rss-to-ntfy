Periodically check RSS feeds and send notifications to `ntfy` topics when new items are published.

## Building

To build the executable:

```
go build -v -ldflags="-w -s" -o rss-to-ntfy main.go
```

## Configuration

Create a config file (e.g., `feeds.yaml`) with the following structure:

```yaml
feeds:
  - url: https://example.com/rss
    ntfy_topic: https://ntfy.sh/your-topic
  - url: https://another-site.com/feed
    ntfy_topic: https://ntfy.sh/another-topic
```

## Usage

Run the program with the config and desired check interval:

```
./rss-to-ntfy -config feeds.yaml -interval 10m
```

This will check the configured feeds every 10 minutes. You can use any valid Go duration string (e.g., 30s, 1h, etc.).
