#!/usr/bin/env bash
set -ueo pipefail

# Copyright (C) 2016-2017  Vincent Ambo <mail@tazj.in>
#
# This file is part of Kontemplate.
#
# Kontemplate is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.

readonly TAG=$(date '+%y%m%d.%H%M')
readonly GIT_HASH="$(git rev-parse --short HEAD)"
readonly LDFLAGS="-X main.gitHash=${GIT_HASH} -X main.version=${TAG} -w -s"

function binary-name() {
    local os="${1}"
    local target="${2}"
    if [ "${os}" = "windows" ]; then
        echo -n "${target}/kontemplate.exe"
    else
        echo -n "${target}/kontemplate"
    fi
}

function build-for() {
    local os="${1}"
    local arch="${2}"
    local target="release/${os}/${arch}"
    local bin=$(binary-name "${os}" "${target}")

    echo "Building kontemplate for ${os}-${arch} in ${target}"

    mkdir -p "${target}"

    env GOOS="${os}" GOARCH="${arch}" go build \
        -ldflags "${LDFLAGS}" \
        -o "${bin}" \
        -tags netgo
}

function sign-for() {
    local os="${1}"
    local arch="${2}"
    local target="release/${os}/${arch}"
    local bin=$(binary-name "${os}" "${target}")
    local VERSION=$(release/linux/amd64/kontemplate version)
    local tar="release/kontemplate-${VERSION}-${os}-${arch}.tar.gz"

    echo "Packing release into ${tar}"
    tar czvf "${tar}" -C "${target}" $(basename "${bin}")

    local hash=$(sha256sum "${tar}")
    echo "Signing kontemplate release tarball for ${os}-${arch} with SHA256 ${hash}"
    gpg --armor --detach-sig --sign "${tar}"
}

case "${1}" in
    "build")
        # Build releases for various operating systems:
        build-for "linux" "amd64"
        build-for "linux" "arm"
        build-for "darwin" "amd64"
        build-for "darwin" "arm64"
        #build-for "windows" "amd64"
        #build-for "freebsd" "amd64"
        git tag -a "v${TAG}" -m "Release ${TAG}"
        exit 0
        ;;
    "sign")
        # Bundle and sign releases:
        sign-for "linux" "amd64"
        sign-for "darwin" "amd64"
        #sign-for "windows" "amd64"
        #sign-for "freebsd" "amd64"
        exit 0
        ;;
esac
