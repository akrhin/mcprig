## What changes

<!-- behaviour before and after, in one or two sentences -->

## How it was checked

<!-- the command and its result; the full gate for a behaviour change -->

```bash
gofmt -l .                          # prints nothing
go build ./...
go vet ./...
go test ./... -race -count=1 -p 1
```

## Checklist

- [ ] one meaningful change; commits `type(scope): subject` per `CONTRIBUTING.md`
- [ ] `docs/en/` and `docs/ru/` updated with the behaviour, or the body says why
      there is nothing to update
- [ ] `CHANGELOG.md` extended under `## [Unreleased]`
- [ ] no secrets or tokens in the diff or the logs
- [ ] `Closes #N`
