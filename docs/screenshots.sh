#!/usr/bin/env bash
# Regenerates docs/images/*.png: builds agentpr, runs it in a detached tmux on a demo repo
# with a few Threads, and renders each screen with freeze. Needs go, git, tmux and ImageMagick.
# A fake `claude` drafts the commit message and a fake `gh` reports no PR, so nothing real is called.
set -euo pipefail
cd "$(dirname "$0")/.."
out=$PWD/docs/images
tmp=$(mktemp -d)
sock=agentpr-shots-$$
trap 'tmux -L "$sock" kill-server 2>/dev/null || true; rm -rf "$tmp"' EXIT
mkdir -p "$out" "$tmp/bin" "$tmp/home"

go build -o "$tmp/bin/agentpr" .
GOBIN=$tmp go install github.com/charmbracelet/freeze@v0.2.2
ln -s "$(command -v git)" "$tmp/bin/git"
cat >"$tmp/bin/claude" <<'EOF'
#!/bin/sh
printf 'feat(server): rate-limit requests per client IP\n\nOver 10 requests a second a client gets 429 with Retry-After, so one\nnoisy caller cannot starve the rest.\n'
EOF
cat >"$tmp/bin/gh" <<'EOF'
#!/bin/sh
echo 'no pull requests found for branch "feature/rate-limit"' >&2
exit 1
EOF
chmod +x "$tmp/bin/claude" "$tmp/bin/gh"

# The demo repo: main has a tiny HTTP service, feature/rate-limit adds a rate limiter
# (committed) and a README line (uncommitted). origin is a local bare repo.
export HOME=$tmp/home GIT_CONFIG_NOSYSTEM=1
demo=$tmp/rate-limit
git init -q --bare "$tmp/origin.git"
git init -q -b main "$demo"
cd "$demo"
git config user.name Demo
git config user.email demo@example.com
git remote add origin "$tmp/origin.git"
printf '# hello\n\nA tiny HTTP service.\n' >README.md
cat >server.go <<'EOF'
package main

import (
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", hello)
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func hello(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello\n"))
}
EOF
git add -A
git commit -qm "Add hello service"
git push -q origin main
git switch -qc feature/rate-limit
cat >server.go <<'EOF'
package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	lim := NewLimiter(10, time.Second)
	mux := http.NewServeMux()
	mux.Handle("/hello", lim.Wrap(http.HandlerFunc(hello)))
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func hello(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello\n"))
}
EOF
cat >limiter.go <<'EOF'
package main

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter is a token bucket per client IP.
type Limiter struct {
	mu      sync.Mutex
	rate    int
	every   time.Duration
	buckets map[string]*bucket
}

type bucket struct {
	tokens int
	last   time.Time
}

func NewLimiter(rate int, every time.Duration) *Limiter {
	return &Limiter{rate: rate, every: every, buckets: map[string]*bucket{}}
}

// Allow takes a token from ip's bucket, refilling it first.
func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{tokens: l.rate, last: time.Now()}
		l.buckets[ip] = b
	}
	if n := int(time.Since(b.last) / l.every); n > 0 {
		b.tokens = min(l.rate, b.tokens+n*l.rate)
		b.last = time.Now()
	}
	if b.tokens == 0 {
		return false
	}
	b.tokens--
	return true
}

// Wrap rejects requests over the limit with 429 and a Retry-After header.
func (l *Limiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !l.Allow(ip) {
			w.Header().Set("Retry-After", strconv.Itoa(int(l.every.Seconds())))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
EOF
git add -A
git commit -qm "Add a per-IP rate limiter"
printf '\nRequests are limited to 10 a second per client IP; over that, the server answers 429.\n' >>README.md

# Threads, written as the Reviewer would leave them; Claude answers two with `agentpr reply`.
at() { grep -nF -- "$2" "$1" | head -1 | cut -d: -f1; }
now=2026-10-18T10:00:00Z
mkdir -p .git/agentpr
cat >.git/agentpr/feature__rate-limit.json <<EOF
{"next_id": 4, "threads": [
 {"id": "t1", "path": "limiter.go", "line": $(at limiter.go 'if b.tokens == 0'), "lines": ["+\tif b.tokens == 0 {", "+\t\treturn false"],
  "comments": [{"author": "reviewer", "body": "A rejected client gets no hint when to come back. Can we tell them?", "at": "$now"}]},
 {"id": "t2", "path": "limiter.go", "line": $(at limiter.go 'buckets map'), "lines": ["+\tbuckets map[string]*bucket"],
  "comments": [{"author": "reviewer", "body": "This map grows by one entry per IP, forever.", "at": "$now"}]},
 {"id": "t3", "path": "server.go", "line": $(at server.go 'NewLimiter'), "lines": ["+\tlim := NewLimiter(10, time.Second)"], "resolved": true,
  "comments": [{"author": "reviewer", "body": "Make the limit a flag?", "at": "$now"}, {"author": "author", "body": "Added -rate.", "at": "$now"}]},
 {"id": "t4", "path": "README.md",
  "comments": [{"author": "reviewer", "body": "Mention the Retry-After header here too.", "pending": true, "at": "$now"}]}
]}
EOF
PATH=$tmp/bin agentpr reply t1 "Wrap now sets Retry-After to the refill interval before answering 429, so clients know when to retry." >/dev/null
PATH=$tmp/bin agentpr reply t2 "True. For this service the set of IPs is small; I can evict buckets idle for a minute if you want." >/dev/null

tm() { tmux -L "$sock" "$@"; }
# wait_for: until the pane shows text, or fail after 10s.
wait_for() {
	for _ in $(seq 50); do
		tm capture-pane -p -t "$1" | grep -qF -- "$2" && return
		sleep 0.2
	done
	echo "timed out waiting for '$2' in $1" >&2
	tm capture-pane -p -t "$1" >&2
	exit 1
}

# shot <name> <agentpr flags> <text to wait for> [keys…] writes docs/images/<name>.png;
# each key is typed as text, except Enter.
shot() {
	local name=$1 flags=$2 until=$3
	shift 3
	tm new-session -d -s "$name" -x 140 -y 35 -c "$demo" \
		"env -i HOME=$HOME GIT_CONFIG_NOSYSTEM=1 PATH=$tmp/bin TERM=xterm-256color COLORTERM=truecolor $tmp/bin/agentpr $flags"
	wait_for "$name" "Changeset vs"
	for k in "$@"; do
		if [ "$k" = Enter ]; then tm send-keys -t "$name" Enter; else tm send-keys -t "$name" -l "$k"; fi
		sleep 0.2
	done
	wait_for "$name" "$until"
	sleep 0.5
	# One line at a time: tmux carries colors over from the previous line, freeze starts each line fresh.
	for y in $(seq 0 34); do tm capture-pane -e -N -p -t "$name" -S "$y" -E "$y"; done >"$tmp/$name.ansi"
	tm kill-session -t "$name"
	"$tmp/freeze" --language ansi --font.ligatures=false --window --padding 20 --margin 0 --border.radius 8 \
		--font.size 14 -o "$tmp/$name.png" <"$tmp/$name.ansi" >/dev/null
	# freeze renders at 4x; halve it and cut to 256 colors to keep the PNGs small.
	magick "$tmp/$name.png" -resize 50% -dither None -colors 256 "PNG8:$out/$name.png"
	echo "docs/images/$name.png"
}

# G then a search scrolls the match to the top of the screen.
shot diff "" "claude:" G / "Allow takes" Enter
shot thread "" "add a ponytail" G / "buckets map" Enter j Enter "Fine for now, add a ponytail: comment."
shot commit "" "feat(server)" c
shot latte "--theme catppuccin-latte" "claude:" G / "Allow takes" Enter
