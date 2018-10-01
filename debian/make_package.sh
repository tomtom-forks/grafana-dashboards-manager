#!/bin/bash
set -ex

export DEBIAN_VERSION=$(date +"1.%Y.%m.%d.%H.%M.%S")
export BASE=$(dirname $0)
export GIT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"

[ -f ${BASE}/changelog ] && rm ${BASE}/changelog
dch --newversion=${DEBIAN_VERSION} --create --noquery --distribution stable --package grafana-dashboard-manager "Build grafana-dashboard-manager from branch $GIT_BRANCH"

debuild -e DEBIAN_VERSION -e GOROOT -e GOPATH -e PATH -us -uc -b $@
