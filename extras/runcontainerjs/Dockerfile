FROM node:25 AS dev
COPY . /build/
WORKDIR /build/
RUN npm install
RUN npx webpack

FROM scratch
COPY --from=dev /build/dist/* /
