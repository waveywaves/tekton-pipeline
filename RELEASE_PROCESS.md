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

* Create a PR for the new release in the CI configuration repository <https://github.com/openshift/release>.
  [Look for an example here.](https://github.com/openshift/release/pull/3623). Wait that it gets merged. Make sure you have all the files in there which is one in `ci-operator/config` and two in `ci-operator/job`. Here is a handy script where you just need to change the version manually (also base it on latest released version like 0.4.0) :

  ```bash
   % for i in $(find . -name '*tektoncd*0.4.0*');do sed -e "s/0\\\.4\\\.0/0\\\.5\\\.2/g" -e "s/0.4.0/0.5.2/" $i > ${i/0.4.0/0.5.2};done
  ```

* Create a PR in <https://github.com/openshift/tektoncd-pipeline> against `openshift/release-v${RELEASE}` for CI to pickup. The base branch for this PR will be against `openshift/tektoncd-pipeline:release-v${VERSION}`. See here for an [example](https://github.com/openshift/tektoncd-pipeline/pull/26).

* You can create a dummy commit in there, the only purpose is to make the CI running and start generating the images.

* If there is a new binary generated for docker images then make sure you follow the section [New Images](#new-images) of this document

* Make sure CI has picked up in your new PR and if that succedeed it means you have setup things successfully 🎉

### Generate release.yaml

* Create a branch based on `openshift/release-v${RELEASE}` and run the command :

`make generate-release RELEASE_VERSION=${RELEASE}`

This will generate a file in `openshift/release/tektoncd-pipeline-${RELEASE}.yaml` which you can add and create a PR for it (against the `openshift/tektoncd-pipeline` repository and `release-v${RELEASE}` branch)

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


### Tagging

TODO:
