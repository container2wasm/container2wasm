# Examples 

Examples of container-to-wasm conversion and running the WASM image on WASI runtime and browser.

Please refer to [README](./../README.md) for basic examples (e.g. Ubuntu on WASM and browser).

- [`./wasi-browser`](./wasi-browser/): Running container on browser by WASI-on-browser Polyfill.
- [`./emscripten`](./emscripten/): Running container on browser by emscripten and [QEMU Wasm](https://github.com/ktock/qemu-wasm).
- [`./emscripten-simple`](./emscripten-simple/): Smaller example of running container on browser by emscripten and [QEMU Wasm](https://github.com/ktock/qemu-wasm).
- [`./python-x86_64`](./python-x86_64/): Running x86_64 Python container on the wasm runtime and browser.
- [`./python-riscv64`](./python-riscv64/): Running RISC-V Python container on the wasm runtime and browser.
- [`./php-x86_64`](./php-x86_64/): Running x86_64 PHP container on the wasm runtime and browser.
- [`./php-riscv64`](./php-riscv64/): Running RISC-V PHP container on the wasm runtime and browser.
- [`./networking`](./networking/): Running container on WASM with networking support.
- [`./no-conversion-wasi-browser`](./no-conversion-wasi-browser/): Running container on browser without pre-conversion of container images, with WASI-on-browser approach.
- [`./no-conversion-emscripten`](./no-conversion-emscripten/): Running container on browser without pre-conversion of container images, using QEMU inside browser.
- [`./raspi3ap-qemu`](./raspi3ap-qemu): Raspberry Pi board emulation on browser
- [`./llm-agent`](./llm-agent): Using container inside browser as an execution environment for LLM agents
