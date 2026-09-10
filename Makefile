VERSION ?= dev

.PHONY: build test install check-spawn check-fmt check

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/hop ./cmd/hop

test:
	go test ./... -short

install: build
	install -m 0755 bin/hop $(HOME)/.local/bin/hop

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
