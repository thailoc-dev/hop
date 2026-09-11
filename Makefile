VERSION ?= dev

.PHONY: build test install check-spawn check-fmt check

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/hop ./cmd/hop

test:
	go test ./... -short

install: build
	install -m 0755 bin/hop $(HOME)/.local/bin/hop
	@# A different hop earlier on PATH would silently shadow this one. Twice a
	@# stale copy has been mistaken for a bug; say so at the moment it matters.
	@resolved=$$(command -v hop 2>/dev/null); \
	if [ -n "$$resolved" ] && [ "$$resolved" != "$(HOME)/.local/bin/hop" ]; then \
		echo "WARNING: 'hop' on your PATH resolves to $$resolved, not the one just installed."; \
		echo "         Remove it or reorder PATH, or the next run will not be this build."; \
	fi

# The rule is about production code: nothing outside sshexec, sysprobe and
# connect.go (where hop re-executes itself) may spawn a process. Test files are
# exempt -- an end-to-end test spawning the binary is the point of one.
check-spawn:
	@! grep -rn "exec.Command" --include='*.go' internal/ cmd/ \
		| grep -v '_test\.go:' \
		| grep -vE "^internal/(sshexec|sysprobe)/|^cmd/hop/connect\.go" \
		|| (echo "exec.Command in production code outside sshexec/sysprobe/connect.go" && exit 1)

check-fmt:
	@unformatted=$$(gofmt -l cmd internal); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi

check: check-fmt check-spawn
	go vet ./...
	go test ./... -short -race
	go test ./cmd/hop/ -run TestEndToEnd
