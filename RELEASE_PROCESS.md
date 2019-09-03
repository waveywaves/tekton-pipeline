# How to release an OpenShift downstream release

## Prereq

* The variable ${RELEASE} is the tekton upstream version for example `0.3.1`

* You need to make sure to have your remote properly setup, one called `upstream` against [tektoncd/pipeline](https://github.com/tektoncd/pipeline) and one called `openshift` against [openshift/tektoncd-pipeline](https://github.com/openshift/tektoncd-pipeline)

* `yq` [CLI tool is installed](https://mikefarah.github.io/yq/)

## Steps

* Generate a new branch and push it to <https://github.com/openshift/tektoncd-pipeline> with this [script](https://github.com/openshift/tektoncd-pipeline/blob/master/openshift/release/create-release-branch.sh), for example like this :

  ```bash
  % bash -ex ./openshift/release/create-release-branch.sh v${RELEASE} release-v${RELEASE}
  ```

* push the new branch to GitHub (you will need write access to the repo):

  ```bash
  % git push openshift release-v${RELEASE}
  ```

* Create a PR for the new release in the CI configuration repository <https://github.com/openshift/release>.
  [Look for an example here.](https://github.com/openshift/release/pull/3623). Wait that it gets merged. Make sure you have all the files in there which is two in `ci-operator/config` and one in `ci-operator/job`. Here is a handy script where you just need to change the version manually (also base it on latest released version like 0.4.0) :

  ```bash
   % for i in $(find . -name '*tektoncd*0.4.0*');do sed -e "s/0\\\.4\\\.0/0\\\.5\\\.2/g" -e "s/0.4.0/0.5.2/" $i > ${i/0.4.0/0.5.2};done
  ```

* Create a PR in <https://github.com/openshift/tektoncd-pipeline> against `openshift/release-v${RELEASE}` for CI to pickup. The base branch for this PR will be against `openshift/tektoncd-pipeline:release-v${VERSION}`. See here for an [example](https://github.com/openshift/tektoncd-pipeline/pull/26).

* You can create a dummy commit in there, the only purpose is to make the CI running and start generating the images.

* If there is a new binary generated for docker images then you will have to add them,  just to add it to the target variable CORE_IMAGES in the Makefile and rerun the makefile target `make generate-dockerfiles`  see [here](https://github.com/openshift/tektoncd-pipeline/pull/37/commits/7eb33c2348eb5c2cbde65e975607e14eb7ccbb23) for an example and an example [here](https://github.com/openshift/release/pull/3916/commits/6289f1b85d0422f2c043541bc3d70f0bab6a1e87) for the `openshift/release` repository so prow will build the image for this new binary.

* Make sure CI has picked up in your new PR and if that succedeed it means you have setup things successfully 🎉

### Generate release.yaml

* Create a branch based on `openshift/release-v${RELEASE}` and run the command :

`make generate-release RELEASE_VERSION=${RELEASE}`

This will generate a file in `openshift/release/tektoncd-pipeline-${RELEASE}.yaml` which you can add and create a PR for it (against the `openshift/tektoncd-pipeline` repository and `release-v${RELEASE}` branch)

### Tagging

TODO:
