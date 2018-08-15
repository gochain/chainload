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

version_file="version.go"
# Bump version, patch by default - also checks if previous commit message contains `[bump X]`, and if so, bumps the appropriate semver number - https://github.com/treeder/dockers/tree/master/bump
docker run --rm -it -v $PWD:/app -w /app treeder/bump --filename $version_file "$(git log -1 --pretty=%B)"
version=$(grep -m1 -Eo "[0-9]+\.[0-9]+\.[0-9]+" $version_file)
echo "Version: $version"

make docker

git add -u
git commit -m "$image: $version release [skip ci]"
git tag -f -a "$version" -m "version $version"
git push
git push origin $version

# Push docker hub images
docker tag $user/$image:latest $user/$image:$version
docker push $user/$image:$version
docker push $user/$image:latest

# Push GCR docker images
echo $ACCT_AUTH > ${HOME}/gcloud-service-key.json
./tmp/google-cloud-sdk/bin/gcloud auth activate-service-account --key-file=${HOME}/gcloud-service-key.json
docker login -u _json_key -p "$(cat ${HOME}/gcloud-service-key.json)" https://gcr.io
docker tag $user/$image:latest gcr.io/$gcr_project/$image:latest
docker tag $user/$image:latest gcr.io/$gcr_project/$image:$version
docker push gcr.io/$gcr_project/$image:latest
docker push gcr.io/$gcr_project/$image:$version
