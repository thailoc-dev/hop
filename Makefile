VERSION ?= dev

.PHONY: build test install check-spawn

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/hop ./cmd/hop

test:
	go test ./... -short

install: build
	install -m 0755 bin/hop $(HOME)/.local/bin/hop

check-spawn:
	@! grep -rn "exec.Command" --include='*.go' internal/ cmd/ \
		| grep -vE "^internal/(sshexec|sysprobe)/" \
		|| (echo "exec.Command outside sshexec/sysprobe" && exit 1)
