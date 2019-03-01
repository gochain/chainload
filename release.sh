#!/bin/bash
set -exuo pipefail

user="gochain"
image="chainload"
gcr_project="gochain-core"

# ensure working dir is clean
git status
if [[ -z $(git status -s) ]]
then
  echo "tree is clean"
else
  echo "tree is dirty, please commit changes before running this"
  exit 1
fi

version=$(git tag --points-at HEAD)
if [[ $version == "" ]]; then
  version=$(git log -1 --format="%h")
fi
echo "Version: $version"

make docker

# Push docker hub images
docker tag $user/$image:latest $user/$image:$version
docker push $user/$image:$version
docker push $user/$image:latest

# Push GCR docker images
./tmp/google-cloud-sdk/bin/gcloud auth activate-service-account --key-file=${HOME}/gcloud-service-key.json
docker tag $user/$image:latest gcr.io/$gcr_project/$image:latest
docker tag $user/$image:latest gcr.io/$gcr_project/$image:$version
docker push gcr.io/$gcr_project/$image:latest
docker push gcr.io/$gcr_project/$image:$version
