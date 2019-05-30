# How to release an OpenShift downstream release

## Prereq

* The variable ${RELEASE} is the tekton upstream version for example `0.3.1`

* You need to make sure to have your remote properly setup, called `upstream` against [tektoncd/pipeline](https://github.com/tektoncd/pipeline) and `openshift` against [openshift/tektoncd-pipeline](https://github.com/openshift/tektoncd-pipeline)

## Steps

* Generate a new branch and push it to <https://github.com/openshift/tektoncd-pipeline> with this [script.](https://github.com/openshift/tektoncd-pipeline/blob/master/openshift/release/create-release-branch.sh) for example with :

  ```bash
  % bash -ex ./openshift/release/create-release-branch.sh v${RELEASE} release-v${RELEASE}
  ```

* push the new branch to GitHub:

  ```bash
  % git push openshift release-v${RELEASE}
  ```

* Create a PR for new release $RELEASE in <https://github.com/openshift/release> for the new version,
  [look here for an example.](https://github.com/openshift/release/pull/3623). And wait that it gets merged. Make sure you have all the files in there which is two in `ci-operator/config` and one in `ci-operator/job`. Here is a handy script where you just need to change the version manually :

  ```bash
   % for i in **/*tektoncd*0.3.0*;do sed -e "s/0\\\.3\\\.0/0\\\.4\\\.0/g" -e "s/0.3.0/0.4.0/" $i > ${i/0.3.0/0.4.0};done
  ```

* Create a PR in <https://github.com/openshift/tektoncd-pipeline> against `openshift/release-v${RELEASE}` for CI to pickup. The base branch for this PR will be against `openshift/tektoncd-pipeline:release-v${VERSION}`. See here for an [example](https://github.com/openshift/tektoncd-pipeline/pull/26).

* You can create a dummy commit in there to make the CI running and looking for it.

* If there is new binary generated for docker images then you have to add them, you just need to add it to the target variable CORE_IMAGES in the Makefile and rerun then the `make generate-dockerfiles`  see [here](https://github.com/openshift/tektoncd-pipeline/pull/37/commits/7eb33c2348eb5c2cbde65e975607e14eb7ccbb23) for an example and an example [here](https://github.com/openshift/release/pull/3916/commits/6289f1b85d0422f2c043541bc3d70f0bab6a1e87) for the `openshift/release` so prow will build the image for this new binary.

* Make sure CI has picked up in your new PR and if it succedeed means you have setup things successfully 🎉

### Push generated image for release

* You need to pull images from `registry.svc.ci.openshift.org` and push them to `quay.io/openshift-pipeline/`

* You need first a docker contain to access `registry.svc.ci.openshift.org`, you first go to this [URL](https://api.ci.openshift.org/oauth/token/request) and have a CLI Command and a token to login, you then get the registry token with :

```bash
% oc registry login --to=${HOME}/.docker/config.json
```

* (to be changed soon) You should now be able to pull the latest release from the CI registry. You can start launch this script to pull   the images from CI and push it to quay.io openshift-pipelines repo. (I let the reader setup their quay access in docker config and request access to write in the openshift-pipelines org).  See this PR here: https://github.com/openshift/tektoncd-pipeline/pull/38
