.PHONY: build clean

build:
	GOOS=wasip1 GOARCH=wasm go build -o guard.wasm main.go

clean:
	rm -f guard.wasm
