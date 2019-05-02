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
  % git push openshift release-v{RELEASE}
  ```

* Create a PR for new release $RELEASE in <https://github.com/openshift/release> for the new version,
  [look here for an example.](https://github.com/openshift/release/pull/3623). And wait that it gets merged.

* Create a PR in <https://github.com/openshift/tektoncd-pipeline> against `openshift/release-v${RELEASE}` for CI to pickup. The base branch for this PR will be against `openshift/tektoncd-pipeline:release-v${VERSION}`. See here for an [example](https://github.com/openshift/tektoncd-pipeline/pull/26).

* Make sure CI has picked up in your new PR and if it does it means you have setup things successfully 🎉

### Push generated image for release

* You need to pull images from `registry.svc.ci.openshift.org` and push them to `quay.io/openshift-pipeline/`

* You need first a docker contain to access `registry.svc.ci.openshift.org`, you first go to this [URL](https://api.ci.openshift.org/oauth/token/request) and have a CLI Command and a token to login, you then get the registry token with :

```bash
% oc registry login --to=${HOME}/.docker/config.json
```

* You should now be able to pull the latest release from the CI registry. You can start launch this script to pull   the images from CI and push it to quay.io openshift-pipelines repo. (I let the reader setup their quay access in docker config and request access to write in the openshift-pipelines org).

* You can use this script to do so :

 <https://gist.github.com/chmouel/664095ecb84271b69bf2b9fccc78e5e8>

 it should take all images for $RELEASE, tag them on latest and push them.
