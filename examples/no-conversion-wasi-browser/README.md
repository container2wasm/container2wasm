# Running container on browser without pre-conversion of the container image

This is an example to run a container on browser without pre-conversion of the container imgae.

[`imagemounter`](./extras/imagemounter/) helper enables to directly mount a container image into the emulated Linux VM on Wasm, without container-to-wasm pre-conversion.

> NOTE: Refer to [`../../examples/wasi-browser/`](../../examples/wasi-browser/) for the basics of WASI-on-browser approach.

> NOTE: Refer to [`../no-conversion-emscripten/`](../no-conversion-emscripten/) for emscripten (`--to-js` flag) approach.

## Example

> Run this at the project repo root directory.

The following outputs a Wasm image `out.wasm` that contains runc + Linux + CPU emulator, etc. but doesn't contain container image.

```console
$ c2w --external-bundle out.wasm
```

> container2wasm >= 0.6.0 is needed.

Then, put a container image to the server in the standard [OCI Image Layout](https://github.com/opencontainers/image-spec/blob/v1.0.2/image-layout.md).
The following puts `ubuntu:22.04` container image to `/tmp/imageout/`.

```console
$ IMAGE=ubuntu:22.04
$ mkdir /tmp/imageout/
$ docker buildx create --name container --driver=docker-container
$ echo "FROM $IMAGE" | docker buildx build --builder=container --output type=oci,dest=- - | tar -C /tmp/imageout/ -xf -
```

The following runs the image on browser.
The above OCI image is located at `http://localhost:8083/ubuntu-22.04/`.
That image can run on browser via `http://localhost:8083/?image=http://localhost:8083/ubuntu-22.04/` that fetches the container image into the browser and launches it.

```console
$ mkdir -p /tmp/out-js3/
$ cp -R ./examples/no-conversion-wasi-browser/* /tmp/out-js3/
$ cp -R /tmp/imageout /tmp/out-js3/htdocs/ubuntu-22.04
$ make imagemounter.wasm && cat ./out/imagemounter.wasm | gzip >  /tmp/out-js3/htdocs/imagemounter.wasm.gzip
$ cat out.wasm | gzip >  /tmp/out-js3/htdocs/out.wasm.gzip
$ ( cd extras/runcontainerjs ; npm install && npx webpack && cp -R ./dist /tmp/out-js3/htdocs/ )
$ docker run --rm -p 127.0.0.1:8083:80 \
         -v "/tmp/out-js3/htdocs:/usr/local/apache2/htdocs/:ro" \
         -v "/tmp/out-js3/xterm-pty.conf:/usr/local/apache2/conf/extra/xterm-pty.conf:ro" \
         --entrypoint=/bin/sh httpd -c 'echo "Include conf/extra/xterm-pty.conf" >> /usr/local/apache2/conf/httpd.conf && httpd-foreground'
```

For more details about imagemounter and other examples (e.g. eStargz, container registry for image distribution, etc.), please see also [`../../extras/imagemounter/README.md`](./../../extras/imagemounter/README.md).
