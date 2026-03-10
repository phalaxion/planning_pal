GOOS ?= darwin
GOARCH ?= amd64

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o bin/server-$(GOOS)-$(GOARCH) ./cmd

run:
	go run ./cmd

fmt:
	gofmt -w .

deploy:
	scp bin/server-$(GOOS)-$(GOARCH) root@david-b.devdotwms.com:/opt/planning-pal/server
	scp -r frontend/* root@david-b.devdotwms.com:/var/www/planning-pal/
	ssh root@david-b.devdotwms.com "systemctl restart planning-pal"