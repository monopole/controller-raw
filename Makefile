GOFILES:=$(shell find . -name '*.go' | grep -v -E '(./vendor)')

all: bin/reboot-agent bin/reboot-controller

GVERSION=$(shell $(CURDIR)/git-version.sh)

# images: GVERSION=$(shell $(CURDIR)/git-version.sh)
images: bin/reboot-agent bin/reboot-controller
	echo $GVERSION
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

#	curl -X DELETE -u "$user:$pass" https://index.docker.io/v1/repositories/monopole/reboot-agent/

bin/%: LDFLAGS=-X github.com/monopole/controller-raw/common.Version=$(shell $(CURDIR)/git-version.sh)
bin/%: $(GOFILES)
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux \
	go build -ldflags "$(LDFLAGS)" -o $@ -a -installsuffix cgo $(notdir $@)/main.go

#	mkdir -p $(dir $@)
#	GOOS=$(word 1, $(subst /, ,$*)) GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $@ github.com/monopole/controller-raw/$(notdir $@)


.PHONY: dockerrun
dockerrun:
	docker run -d reboot-agent:
	docker ps # grab the containerID and use it below
	docker logs 95987a765ade
