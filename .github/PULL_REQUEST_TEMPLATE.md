**What this changes and why**

**Checklist**
- [ ] `go build ./... && go vet ./... && gofmt -l . && go test ./...` passes
- [ ] If `web/` changed: `npm run build` passes and the resulting
      `internal/webui/app/dist` diff is included in this PR
- [ ] Focused change — one thing, not a drive-by refactor of unrelated code
