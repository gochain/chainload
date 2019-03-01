#!/bin/bash
version=$(git tag --points-at HEAD)
if [[ $version == "" ]]; then
  version=$(git log -1 --format="%h")
fi
echo $version