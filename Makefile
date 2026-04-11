# Remote deploy: set REMOTE_HOST (and optionally REMOTE_USER, REMOTE_DIR, GOARCH).
# Optional local overrides: `-include Makefile.local` (gitignored).

-include Makefile.local

REMOTE_USER ?= ayush
REMOTE_HOST ?= machine
REMOTE_DIR ?= sandbox
GOARCH ?= amd64

REMOTE := $(REMOTE_USER)@$(REMOTE_HOST)
REMOTE_BASE := ssh -o BatchMode=yes $(REMOTE)
REMOTE_CD := cd /home/$(REMOTE_USER)/$(REMOTE_DIR)

# Passed to remote-run, e.g. `make remote-run REMOTE_HOST=h ARGS='doctor'`
ARGS ?= doctor

BENCH_CONFIG ?= configs/example.json
BENCH_ITERS ?= 3

BENCH_CONCURRENCY ?= 5

.PHONY: build build-linux sync sync-all remote-shell remote-check remote-setup remote-run remote-test remote-bench remote-bench-snapshot remote-bench-concurrent

build:
	go build ./...

build-linux:
	mkdir -p bin
	GOOS=linux GOARCH=$(GOARCH) CGO_ENABLED=0 go build -o bin/sandboxbench ./cmd/sandboxbench

check-remote:
	@test -n "$(REMOTE_HOST)" || (echo "set REMOTE_HOST (e.g. make sync REMOTE_HOST=my.server)"; exit 1)

sync: check-remote build-linux
	rsync -avz -e ssh \
		--filter=':- .rsync-filter' \
		-R bin/sandboxbench \
		Makefile \
		configs \
		scripts \
		$(REMOTE):/home/$(REMOTE_USER)/$(REMOTE_DIR)/

sync-all: check-remote build-linux
	rsync -avz -e ssh \
		--filter=':- .rsync-filter' \
		./ $(REMOTE):/home/$(REMOTE_USER)/$(REMOTE_DIR)/ \
		--exclude .git

remote-shell: check-remote
	ssh $(REMOTE)

remote-check: check-remote
	$(REMOTE_BASE) '$(REMOTE_CD) && ./scripts/check-env.sh'

remote-setup: sync
	$(REMOTE_BASE) '$(REMOTE_CD) && bash ./scripts/setup-firecracker.sh'
	$(REMOTE_BASE) '$(REMOTE_CD) && bash ./scripts/setup-guest-assets.sh'

remote-run: check-remote
	$(REMOTE_BASE) '$(REMOTE_CD) && ./bin/sandboxbench $(ARGS)'

remote-test: check-remote
	$(REMOTE_BASE) '$(REMOTE_CD) && go test ./...'

remote-bench: check-remote
	$(REMOTE_BASE) '$(REMOTE_CD) && ./bin/sandboxbench bench --config $(BENCH_CONFIG) -n $(BENCH_ITERS) $(BENCH_ARGS)'

remote-bench-snapshot: check-remote
	$(REMOTE_BASE) '$(REMOTE_CD) && ./bin/sandboxbench bench-snapshot --config $(BENCH_CONFIG) -n $(BENCH_ITERS) $(BENCH_ARGS)'

remote-bench-concurrent: check-remote
	$(REMOTE_BASE) '$(REMOTE_CD) && ./bin/sandboxbench bench-concurrent --config $(BENCH_CONFIG) -c $(BENCH_CONCURRENCY) $(BENCH_ARGS)'

remote-bench-all: check-remote
	$(REMOTE_BASE) '$(REMOTE_CD) && ./bin/sandboxbench bench-all --config $(BENCH_CONFIG) -n $(BENCH_ITERS) -c $(BENCH_CONCURRENCY) $(BENCH_ARGS)'
