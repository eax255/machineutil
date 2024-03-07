SOURCES=$(shell find -type f -name \*.go)
GOIMPORTS?=$(shell ./detect-goimports.sh)
BINARIES=$(notdir $(basename $(wildcard cmd/*.go)))
AUTOBUILD_SOURCES=$(SOURCES)

all: $(BINARIES)

go.mod: $(SOURCES)
	@$(GOIMPORTS) -w $(SOURCES)
	@go mod tidy

$(BINARIES): %: cmd/%.go go.mod
	@go build -o $@ $<

autobuild:
	@systemctl --user stop autobuild.service autobuild.path 2>/dev/null ||true
	@systemctl --user reset-failed autobuild.service autobuild.path 2>/dev/null ||true
	@systemd-run --user --unit=autobuild --working-directory=$$PWD  $$(echo $(AUTOBUILD_SOURCES) |xargs -r -n1 dirname |xargs -r -n1 realpath |sort -u |sed 's/^/--path-property=PathModified=/') --path-property=TriggerLimitBurst=2 make all
