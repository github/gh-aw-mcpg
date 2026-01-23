.PHONY: build clean

build:
	@if command -v tinygo >/dev/null 2>&1; then \
		echo "Building with TinyGo..."; \
		tinygo build -o guard.wasm -target=wasi main.go 2>&1 || \
		(echo "TinyGo build failed (may not support Go 1.25), falling back to standard Go..."; \
		 echo "Note: Standard Go WASM may not properly export functions"; \
		 GOOS=wasip1 GOARCH=wasm go build -o guard.wasm main.go); \
	else \
		echo "TinyGo not found, using standard Go (function exports may not work)"; \
		echo "For proper WASM guard support, install TinyGo: https://tinygo.org"; \
		GOOS=wasip1 GOARCH=wasm go build -o guard.wasm main.go; \
	fi

clean:
	rm -f guard.wasm
