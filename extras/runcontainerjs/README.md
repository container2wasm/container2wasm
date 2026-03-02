# runcontainer

JS helper library for running containers on browser, used for some examples in this project.

Container is expected to run in a web worker.

## Example

### emscripten

See [`./../../examples/no-conversion-emscripten/`](./../../examples/no-conversion-emscripten/) for running example.

The following starts the container.

> This example uses xterm-pty for the terminal of the container.

```js
const vmImage = location.origin + "/img";
const outJsAddr = vmImage + "/out.js";
const argModuleJsAddr = vmImage + "/arg-module.js";
const loadJsAddr = vmImage + "/load.js";
const mounterImage = location.origin + "/imagemounter.wasm.gzip";
const stackWorkerFile = location.origin + "/dist/stack-worker.js";
const containerImageAddress = getImageParam();
Module = await RunContainer.createContainerQEMUWasm(Module, outJsAddr, containerImageAddress, stackWorkerFile, mounterImage, argModuleJsAddr, loadJsAddr, (p) => vmImage + "/" + p);
```

### WASI-on-browser

See [`./../../examples/no-conversion-wasi-browser/`](./../../examples/no-conversion-wasi-browser/) for running example.

Prepare container in the main worker:

> This example uses xterm-pty for the terminal of the container.

```js
const worker = new Worker("./worker.js");
const vmImage = location.origin + "/out.wasm.gzip";
const mounterImage = location.origin + "/imagemounter.wasm.gzip";
const stackWorkerFile = location.origin + "/dist/stack-worker.js";
const containerImageAddress = getImageParam();
const infoP = RunContainer.createContainerWASI(vmImage, containerImageAddress, stackWorkerFile, mounterImage);
infoP.then((info) => {
    worker.postMessage({type: "init", info: info, args: ['/bin/sh']});
    new TtyServer(slave).start(worker);
})
```

Start container in a worker:

```js
var ttyClient = new TtyClient(msg.data);
RunContainer.startContainer(info, args, ttyClient);
```
