.PHONY: build clean

build:
	@echo "Building WASM guard..."
	@if command -v tinygo >/dev/null 2>&1; then \
		echo "TinyGo found, attempting build..."; \
		for go_bin in go1.23 go1.23.9 go1.23.10 go1.23.8; do \
			if command -v $$go_bin >/dev/null 2>&1; then \
				echo "Found $$go_bin, using for TinyGo..."; \
				GOROOT=$$($$go_bin env GOROOT) tinygo build -o guard.wasm -target=wasi main.go 2>&1 && \
				echo "✓ Successfully built guard with TinyGo + $$go_bin" && exit 0; \
			fi; \
		done; \
		echo "No Go 1.23 found. Trying TinyGo with system Go..."; \
		tinygo build -o guard.wasm -target=wasi main.go 2>&1 && \
		echo "✓ Successfully built guard with TinyGo" && exit 0; \
		echo "TinyGo build failed (likely Go version incompatibility)"; \
		echo "Install Go 1.23: go install golang.org/dl/go1.23.9@latest && go1.23.9 download"; \
	else \
		echo "TinyGo not found. Install from: https://tinygo.org"; \
	fi; \
	echo "Falling back to standard Go (function exports won't work)..."; \
	GOOS=wasip1 GOARCH=wasm go build -o guard.wasm main.go; \
	echo "⚠ Warning: Guard compiled with standard Go won't export functions properly"

clean:
	rm -f guard.wasm
