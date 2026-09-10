package tunnel

import "strings"

// Class is what kind of failure ended a tunnel attempt. It decides whether
// the supervisor retries or gives up, so it is derived from ssh's stderr
// rather than its exit code: ssh exits 255 for nearly every failure.
type Class int

const (
	ClassUnknown Class = iota
	ClassAuth
	ClassConfig
	ClassLocalPort
	ClassNetwork
	ClassContainer
)

func (c Class) String() string {
	switch c {
	case ClassAuth:
		return "auth"
	case ClassConfig:
		return "config"
	case ClassLocalPort:
		return "local-port"
	case ClassNetwork:
		return "network"
	case ClassContainer:
		return "container"
	default:
		return "unknown"
	}
}

// Fatal reports whether retrying is pointless. Credentials, hostnames and
// busy local ports do not fix themselves; networks and containers do.
func (c Class) Fatal() bool {
	switch c {
	case ClassAuth, ClassConfig, ClassLocalPort:
		return true
	default:
		return false
	}
}

// classRules are checked in order, so fatal patterns are listed first: a
// retryable line appearing alongside a fatal one must not mask it.
var classRules = []struct {
	class    Class
	patterns []string
}{
	{ClassAuth, []string{
		"permission denied",
		"too many authentication failures",
		"host key verification failed",
	}},
	{ClassConfig, []string{
		"could not resolve hostname",
		"name or service not known",
	}},
	{ClassLocalPort, []string{
		"address already in use",
		"cannot listen to port",
	}},
	{ClassContainer, []string{
		"no such object",
		"no such container",
	}},
	{ClassNetwork, []string{
		"connection refused",
		"connection timed out",
		"operation timed out",
		"no route to host",
		"network is unreachable",
		"connection closed by remote host",
		"broken pipe",
		"not responding",
	}},
}

// Classify maps ssh or docker stderr onto a Class. Unrecognised output is
// ClassUnknown, which the state machine retries a bounded number of times
// before giving up.
func Classify(stderr string) Class {
	haystack := strings.ToLower(stderr)
	for _, rule := range classRules {
		for _, pattern := range rule.patterns {
			if strings.Contains(haystack, pattern) {
				return rule.class
			}
		}
	}
	return ClassUnknown
}
