#!/bin/bash

set -exo pipefail

REPO_PATH=$GOPATH/src/github.com/grafana/grafana
mkdir -p $(dirname ${REPO_PATH})
ln -s ${HOME}/workspace ${REPO_PATH}
cd ${REPO_PATH}

# Setup dependancies
/tmp/bootstrap.sh
./scripts/build/download-phantomjs.sh
# Load ruby, needed for packing with fpm
source /etc/profile.d/rvm.sh

# Build everything
CCX64=/tmp/x86_64-centos6-linux-gnu/bin/x86_64-centos6-linux-gnu-gcc
OPT="-includeBuildId=false"
CC=${CCX64} go run build.go ${OPT} build
yarn install --pure-lockfile --no-progress

if [ -d "dist" ]; then
  rm -rf dist
fi

echo "Building frontend"
go run build.go ${OPT} build-frontend

echo "Packaging"
go run build.go -goos linux -pkg-arch amd64 ${OPT} package-only latest

# Compute hashes
go run build.go sha-dist

# Delete unneeded packages
rm dist/grafana*.rpm*
rm dist/grafana*.deb*
