# go-sn

Last verified: 2026-03-20

Go library for reading and writing Supernote `.note` files.

## Tech Stack
- Language: Go 1.24
- Module: `github.com/jdkruzr/go-sn`
- No external dependencies (stdlib only)

## Commands
```bash
go build -C /path/to/go-sn ./...
go test -C /path/to/go-sn ./...
go vet -C /path/to/go-sn ./...
```

## Bash Commands: No `cd &&` Compounds

**NEVER** use `cd /path && command` compound bash statements. Use `go -C /path` or absolute paths instead.

## Project Structure
- `note/` -- core library: parse, render, decode, write .note files (see domain CLAUDE.md)
- `cmd/snrender/` -- CLI: renders .note pages to JPEG images (-bbox for bounding boxes)
- `cmd/sndump/` -- CLI: debug dump of TOTALPATH objects, footer tags, keyword blocks
- `testdata/` -- sample .note files for tests
- `docs/implementation-plans/` -- design docs for features

## Conventions
- Functional Core / Imperative Shell pattern
- All parsing operates on `[]byte` raw file data; mutations return new `[]byte`
- .note binary format: length-prefixed blocks with XML-like `<TAG:VALUE>` metadata tags
- Footer at end of file: 4-byte "tail" marker + 4-byte LE footer offset
