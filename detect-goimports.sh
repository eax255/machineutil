#!/bin/sh
GOIMPORTS=${GOIMPORTS:-$(which goimports > /dev/null 2>/dev/null)}
GOBIN=${GOBIN:-$(go env GOBIN)}
GOBIN=${GOBIN:-$(go env GOPATH)/bin}
GOIMPORTS=${GOIMPORTS:-${GOBIN}/goimports}
echo "$GOIMPORTS"
