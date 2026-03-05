# WingNews

A fast, dark-mode Hacker News reader built with Go and HTMX.

Live at [news.wingman.actor](https://news.wingman.actor)

## Development

### Prerequisites

- Go 1.25+
- [Bun](https://bun.sh) (for Tailwind CSS builds)
- [Air](https://github.com/air-verse/air) (optional, for live reload)

### Run locally

```sh
bun install

bun run css:build

go run .
```

The server starts on `http://localhost:3000` by default. Set the `PORT` environment variable to change it.

### Live reload

With Air installed, run `air` in the project root. It watches `.go` and `.html` files and rebuilds automatically. Run `bun run css:watch` in a separate terminal for CSS changes.

## Docker

```sh
docker build -t wingnews .
docker run -p 3000:3000 wingnews
```

## License

MIT
