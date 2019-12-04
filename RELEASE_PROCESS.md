
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
  [Look for an example here.](https://github.com/openshift/release/pull/3623). Wait that it gets merged. Make sure you have all the files in there which is one in `ci-operator/config` and two in `ci-operator/job`. Here is a handy script that would take care of almost everything (you need to double check that there is no `release-next` lingering in the files) :

  ```bash
   for i in $(find .|grep '.*tektoncd-pipeline-release-next.*');do RV=$(echo ${RELEASE}|sed 's/\./\\\\./g');sed -e "s/\^release-next/^release-v${RV}/" -e "s/release-next/release-v${RELEASE}/" -e "s/tektoncd-next/tektoncd-v${RELEASE}/" $i > $(echo $i| sed "s/release-next/release-v${RELEASE}/");done
   sed -e "s/nightly/v${RELEASE}/" -e "s/tektoncd-next/tektoncd-v${RELEASE}/g" core-services/image-mirroring/tekton/mapping_tekton_nihghtly_quay > core-services/image-mirroring/tekton/mapping_tekton_v$(echo ${RELEASE}|sed 's/\.[0-9]*$/_quay/;s/\./_/g')
  ```

* Get someone to merge the PR before you go to the next step,

* Create a PR in <https://github.com/openshift/tektoncd-pipeline> against `openshift/release-v${RELEASE}` for CI to pickup. The base branch for this PR will be against `openshift/tektoncd-pipeline:release-v${RELEASE}`. See here for an [example](https://github.com/openshift/tektoncd-pipeline/pull/26). You can run this script to automate all of it :

    ```bash
    USER_REMOTE="youruseremote"
    git checkout -b test-release-v${RELEASE} release-v${RELEASE}
    echo "$(date)" > ci
    git add ci;git commit -m "CITest: v${RELEASE}"
    git push ${USER_REMOTE} test-release-v${RELEASE}
    echo "https://github.com/openshift/tektoncd-pipeline/compare/release-v${RELEASE}...${USER_REMOTE}:test-release-v${RELEASE}?expand=1"
    ```

* After the CI tests passed, it will have the images generated and you can `/close/` the CITEST PR  🎉

### Generate release.yaml

* Create a branch based on `openshift/release-v${RELEASE}` and run the command :

```bash
    USER_REMOTE="youruseremote"
    git checkout -b release-yaml-v${RELEASE} release-v${RELEASE}
    make generate-release RELEASE_VERSION=${RELEASE}
    git add openshift/release/tektoncd-pipeline-v${RELEASE}.yaml
    git commit -m "Releasing release.yaml v${RELEASE}"
    git push ${USER_REMOTE} release-yaml-v${RELEASE}
    echo "https://github.com/openshift/tektoncd-pipeline/compare/release-v${RELEASE}...${USER_REMOTE}:release-yaml-v${RELEASE}?expand=1"
```

* This will generate a file in
  `openshift/release/tektoncd-pipeline-${RELEASE}.yaml` commit and push it to
  your git's USER_REMOTE and then you should have link to make your PR against it.

* When you get it merged then you are good to go!

### Other components

### CLI

* You need to make sure the upstream CLI dependency is updated to the new version.

### Catalog

* Catalog needs to be tagged to the new version in downstream :

    https://github.com/openshift/tektoncd-catalog

    # TODO: To be filed by Vincent,

* Images shipped with catalog needs to be tagged for quay mirroring

    # TODO: TO be filled by Vincent

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
