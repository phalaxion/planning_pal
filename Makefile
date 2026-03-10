PPAL_STORE_PATH ?= ./data
PPAL_STORE_TYPE ?= sqlite

GOOS ?= darwin
GOARCH ?= amd64

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o bin/server-$(GOOS)-$(GOARCH) ./cmd

run:
	PPAL_STORE_PATH=$(PPAL_STORE_PATH) PPAL_STORE_TYPE=$(PPAL_STORE_TYPE) go run ./cmd

fmt:
	gofmt -w .

deploy:
	ssh -O exit -o ControlPath=/tmp/deploy-ssh-%r@%h:%p root@david-b.devdotwms.com 2>/dev/null || true
	ssh -MNf -o ControlPath=/tmp/deploy-ssh-%r@%h:%p root@david-b.devdotwms.com
	ssh -o ControlPath=/tmp/deploy-ssh-%r@%h:%p \
		root@david-b.devdotwms.com \
		"mkdir -p /opt/planning-pal /var/www/planning-pal"
	scp -o ControlPath=/tmp/deploy-ssh-%r@%h:%p \
		bin/server-$(GOOS)-$(GOARCH) \
		root@david-b.devdotwms.com:/opt/planning-pal/server.new
	scp -o ControlPath=/tmp/deploy-ssh-%r@%h:%p \
		-r frontend/* \
		root@david-b.devdotwms.com:/var/www/planning-pal/
	ssh -o ControlPath=/tmp/deploy-ssh-%r@%h:%p \
		root@david-b.devdotwms.com \
		"chmod +x /opt/planning-pal/server.new && mv /opt/planning-pal/server.new /opt/planning-pal/server && systemctl restart planning-pal"
	ssh -O exit -o ControlPath=/tmp/deploy-ssh-%r@%h:%p root@david-b.devdotwms.com