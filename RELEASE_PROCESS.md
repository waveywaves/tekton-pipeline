
# How to release an OpenShift downstream release

## Prereq

* The variable ${RELEASE} is the tekton upstream version for example `0.3.1`

* You need to make sure to have your remote properly setup, one called `upstream` against [tektoncd/pipeline](https://github.com/tektoncd/pipeline) and one called `openshift` against [openshift/tektoncd-pipeline](https://github.com/openshift/tektoncd-pipeline)

## Steps

* Generate a new branch and push it to <https://github.com/openshift/tektoncd-pipeline> with this [script](https://github.com/openshift/tektoncd-pipeline/blob/master/openshift/release/create-release-branch.sh), for example like this :

  ```bash
  % bash -ex ./openshift/release/create-release-branch.sh v${RELEASE} release-v${RELEASE}
  ```

* push the new branch to GitHub (you will need write access to the repo):

  ```bash
  % git push openshift release-v${RELEASE}
  ```

* Go to your [catalog](https://github.com/openshift/tektoncd-catalog) repository and checkout openshift/master with :

```bash
git fetch -a openshift
git checkout -B openshift-master openshift/master
```

* Run the release command :

```bash
bash -ex ./openshift/release/create-release-branch.sh ${RELEASE}
```

* Go to your local [pipelines catalog](https://github.com/openshift/pipelines-catalog) repository and checkout openshift/master with :

```bash
git fetch -a openshift
git checkout -B openshift-master openshift/master
```

* Run the release command :

```bash
bash -ex ./openshift/release/create-release-branch.sh ${RELEASE}
```


This will do the push of the tag of the branch for catalog

* Create a PR for the new release in the CI configuration repository <https://github.com/openshift/release>.
  [Look for an example here.](https://github.com/openshift/release/pull/3623). Wait that it gets merged. Make sure you have all the files in there which is one in `ci-operator/config` and two in `ci-operator/job`. Here is a handy script that would take care of almost everything (you need to double check that there is no `release-next` lingering in the files) :

  Take all files for pipelines on release-next and create a release out of it with the right versioning in the file
  ```bash
  for i in $(find .|grep -E '.*tektoncd-pipeline-release-next.*');do RV=$(echo ${RELEASE}|sed 's/\./\\\\./g');sed -e "s/\^release-next/^release-v${RV}/" -e "s/release-next/release-v${RELEASE}/" -e "s/tektoncd-next/tektoncd-v${RELEASE}/" $i > $(echo $i| sed "s/release-next/release-v${RELEASE}/");done
  ```

  Create a quay mirroring for pipeline
  ```
  sed -e "s/nightly/v${RELEASE}/" -e "s/tektoncd-next/tektoncd-v${RELEASE}/g" core-services/image-mirroring/tekton/mapping_tekton_nightly_quay  > core-services/image-mirroring/tekton/mapping_tekton_v$(echo ${RELEASE}|sed 's/\.[0-9]*$/_quay/;s/\./_/g')
 ```

  Take all files for catalog on release-next and create a release out of it with the right versioning in the file
  ```bash
  BASE_RELEASE=$(echo ${RELEASE}|sed 's/\.[0-9]*$//')
  for i in $(find .|grep -E '.*tektoncd-catalog-release-next.*');do RV=$(echo ${BASE_RELEASE}|sed 's/\./\\\\./g');sed -e "s/\^release-next/^release-v${RV}/" -e "s/release-next/release-v${BASE_RELEASE}/" -e "s/tektoncd-next/tektoncd-v${BASE_RELEASE}/" $i > $(echo $i| sed "s/release-next/release-v${BASE_RELEASE}/");done
  ```

  Create a quay miroring for catalog
 ```
  sed -e "s/nightly/v${RELEASE}/" -e "s/tektoncd-next/tektoncd-v${RELEASE}/g" core-services/image-mirroring/tekton/mapping_tekton_catalog_nightly_quay  > core-services/image-mirroring/tekton/mapping_tekton_catalog_v$(echo ${RELEASE}|sed 's/\.[0-9]*$/_quay/;s/\./_/g')
  ```

* Get someone to merge the PR before you go to the next step,

* Create a PR in <https://github.com/openshift/tektoncd-pipeline> against
  `openshift/release-v${RELEASE}` with the newly generated yaml file.
  You can use the makefile target :

  `make  generate-release RELEASE_VERSION=${RELEASE}`

  This will generates a file in
  `openshift/release/tektoncd-pipeline-${RELEASE}.yaml`. You then create a PR
  against the git branch `openshift/tektoncd-pipeline:release-v${RELEASE}`.

  You can run  this script to automate all of it :

    ```bash
    USER_REMOTE="youruseremote"
    git checkout -b release-yaml-v${RELEASE} release-v${RELEASE}
    make generate-release RELEASE_VERSION=${RELEASE}
    git add openshift/release/tektoncd-pipeline-v${RELEASE}.yaml
    git commit -m "Releasing release.yaml v${RELEASE}"
    git push ${USER_REMOTE} release-yaml-v${RELEASE}
    echo "https://github.com/openshift/tektoncd-pipeline/compare/release-v${RELEASE}...${USER_REMOTE}:release-yaml-v${RELEASE}?expand=1"
    ```

* Create a PR in <https://github.com/openshift/tektoncd-pipeline> against

* When you get it merged then you have now released the new pipeline release, you can then do the other following tasks.

### Other components

### CLI

* You need to make sure the upstream CLI dependency is updated to the new version against `openshift/release-v${BASE_RELEASE}` (`BASE_RELEASE` mean the `RELEASE` without it's last number, i.e: for a RELEASE `0.9.2` BASE_RELEASE would be `0.9`) :

    ```bash
    USER_REMOTE="youruseremote"
    BASE_RELEASE=$(echo ${RELEASE}|sed 's/\.[0-9]*$//')
    git checkout -B test-release-v${BASE_RELEASE} release-v${BASE_RELEASE}
    echo $(date) > ci
    git add ci
    git commit -m "[CITEST] Testing release ${BASE_RELEASE}"
    git push ${USER_REMOTE} test-release-v${BASE_RELEASE}
    echo "https://github.com/openshift/tektoncd-catalog/compare/release-v${BASE_RELEASE}...${USER_REMOTE}:test-release-v${BASE_RELEASE}?expand=1"
    ```

## New Images

You need to make sure that the new release doesn't have new binary which means new images that needs to be shipped, the Makefile should do [a check](https://github.com/openshift/tektoncd-pipeline/blob/02f43d3ef90435c2679b336a0ac9c08ff1d4dd9a/Makefile#L31) to make sure you have it specified in your Makefile. You have three variables in there :

* `CORE_IMAGE` - the CORE images that are auto generated from [openshift/ci-operator/Dockerfile.in](openshift/ci-operator/Dockerfile.in)
* `CORE_IMAGE_WITH_GIT` - Images that needs git installed in there generated from [openshift/ci-operator/Dockerfile-git.in](openshift/ci-operator/Dockerfile.in)
* `CORE_IMAGE_CUSTOM` - Those are not auto generated, it's up to you to put whatever you like/need in there.

When you have add your binary to one of your image you need to add it in the `openshift/release` CI, make a PR that looks like this one :

https://github.com/openshift/release/pull/3916/commits/6289f1b85d0422f2c043541bc3d70f0bab6a1e87

* Add it to the quay mirror image like done here :

https://github.com/openshift/release/blob/master/core-services/image-mirroring/tekton/mapping_tekton_v0_4_quay

* And then go to https://quay.io/organization/openshift-pipeline create a new repo for the new image, i.e: `tektoncd-new-image`, you will have then go to the settings of this new repo and add the bot `openshift-pipelines+dat_bot_tho` in there.

* This robot will be used to setup the quay mirroring, you can see the output of the job mirrorings here:

https://prow.svc.ci.openshift.org/?job=periodic-image-mirroring-tekton*

**Note:** the periodic image mirroring job will pick up images from new release version only after a new pr (eg: the pr with dummy commit) is merged to the new release branch we created [here](#steps)
