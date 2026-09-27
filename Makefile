.PHONY: build build-go build-swift test doctor swift-probe format-swift lint-swift clean

build: build-swift build-go

build-go:
	mkdir -p bin
	go build -o bin/msbx ./cmd/msbx

build-swift:
	mkdir -p bin
	swift build -c release --package-path platform/darwin/vm/swift
	cp "$$(swift build -c release --package-path platform/darwin/vm/swift --show-bin-path)/msbx-vm" bin/msbx-vm

doctor: build
	./bin/msbx doctor

swift-probe: build-swift
	./bin/msbx-vm probe

format-swift:
	./scripts/format-swift.sh

lint-swift:
	./scripts/lint-swift.sh

test:
	go test ./...

clean:
	rm -rf bin
	rm -rf platform/darwin/vm/swift/.build
