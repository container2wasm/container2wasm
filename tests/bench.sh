#!/bin/bash

set -eu -o pipefail

CONTAINER=test-container
TIMEOUT=60m
BENCHTIME=3x

DOCKER_BUILDKIT=1 docker build -f tests/Dockerfile.d/Dockerfile.runner -t $CONTAINER .
docker run --rm -d --privileged -v ${CONTAINER}-cache:/var/lib/docker --name $CONTAINER $CONTAINER
until docker exec $CONTAINER docker version
do
    echo "retrying..."
    sleep 3
done
docker exec -w /test/tests/benchmark/ $CONTAINER go test -timeout $TIMEOUT -benchtime $BENCHTIME -bench=.
docker kill $CONTAINER
