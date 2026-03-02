FROM node:25 AS dev
COPY . /build/
WORKDIR /build/
RUN npx webpack

FROM scratch
COPY --from=dev /build/dist/* /
