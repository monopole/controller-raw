GOFILES:=$(shell find . -name '*.go' | grep -v -E '(./vendor)')

all: bin/reboot-agent bin/reboot-controller

GVERSION=$(shell $(CURDIR)/git-version.sh)

# images: GVERSION=$(shell $(CURDIR)/git-version.sh)
images: bin/reboot-agent bin/reboot-controller
	docker build -f Dockerfile-agent -t reboot-agent:$(GVERSION) .
	docker build -f Dockerfile-controller -t reboot-controller:$(GVERSION) .

check:
	@find . -name vendor -prune -o -name '*.go' -exec gofmt -s -d {} +
	@go vet $(shell go list ./... | grep -v '/vendor/')
	@go test -v $(shell go list ./... | grep -v '/vendor/')

.PHONY: vendor
vendor:
	glide update --strip-vendor
	glide-vc

clean:
	rm -rf bin/*
	docker rmi -f `docker images --filter=reference="reboot-*:*" -q`


bin/%: LDFLAGS=-X github.com/monopole/kube-controller-demo/common.Version=$(shell $(CURDIR)/git-version.sh)
bin/%: $(GOFILES)
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux \
	go build -ldflags "$(LDFLAGS)" -o $@ -a -installsuffix cgo $(notdir $@)/main.go

#	mkdir -p $(dir $@)
#	GOOS=$(word 1, $(subst /, ,$*)) GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $@ github.com/monopole/kube-controller-demo/$(notdir $@)



# This assumes docker login --username=monopole --password-stdin
# enter password then CTRL-D
