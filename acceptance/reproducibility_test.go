package acceptance

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrremote "github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/buildpacks/imgutil"
	"github.com/buildpacks/imgutil/local"
	"github.com/buildpacks/imgutil/remote"
	h "github.com/buildpacks/imgutil/testhelpers"
)

type ImageType string

const (
	remoteImg ImageType = "remote"
	localImg  ImageType = "local"
)

type imageSeed struct {
	baseImg    string
	labelKey   string
	labelVal   string
	envKey     string
	envVal     string
	workingDir string
	layer1     string
	layer2     string
}

func TestAcceptance(t *testing.T) {
	rand.Seed(time.Now().UTC().UnixNano())

	dockerConfigDir := t.TempDir()

	dockerRegistry := h.NewDockerRegistry(h.WithAuth(dockerConfigDir))
	dockerRegistry.Start(t)
	defer dockerRegistry.Stop(t)

	t.Setenv("DOCKER_CONFIG", dockerRegistry.DockerDirectory)

	dockerClient := h.DockerCli(t)
	daemonInfo, err := dockerClient.Info(context.TODO())
	h.AssertNil(t, err)
	daemonOS := daemonInfo.OSType

	runnableBaseImageName := h.RunnableBaseImage(daemonOS)
	h.PullIfMissing(t, dockerClient, runnableBaseImageName)

	testCases := []struct {
		type1 ImageType
		type2 ImageType
	}{
		{
			type1: remoteImg,
			type2: remoteImg,
		},
		{
			type1: localImg,
			type2: localImg,
		},
		{
			type1: localImg,
			type2: remoteImg,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s-%s", tc.type1, tc.type2), func(t *testing.T) {
			seed := imageSeed{
				baseImg:    runnableBaseImageName,
				labelKey:   "label-key-" + h.RandString(10),
				labelVal:   "label-val-" + h.RandString(10),
				envKey:     "env-key-" + h.RandString(10),
				envVal:     "env-val-" + h.RandString(10),
				workingDir: "working-dir-" + h.RandString(10),
				layer1:     createLayer(t, daemonOS),
				layer2:     createLayer(t, daemonOS),
			}
			image1 := createImage(t, tc.type1, dockerRegistry, seed)
			image2 := createImage(t, tc.type2, dockerRegistry, seed)
			compare(t, image1, image2)
		})
	}
}

func createLayer(t *testing.T, osType string) string {
	t.Helper()
	layer, err := h.CreateSingleFileLayerTar(fmt.Sprintf("/new-layer-%s.txt", h.RandString(10)), "new-layer-"+h.RandString(10), osType)
	h.AssertNil(t, err)
	t.Cleanup(func() { os.Remove(layer) })
	return layer
}

func createImage(t *testing.T, imageType ImageType, registry *h.DockerRegistry, seed imageSeed) string {
	t.Helper()
	ref := registry.Host + ":" + registry.Port + "/imgutil-acceptance-" + h.RandString(10)
	img := initImage(t, ref, imageType, seed.baseImg)
	h.AssertNil(t, img.AddLayer(seed.layer1))
	h.AssertNil(t, img.AddLayer(seed.layer2))
	h.AssertNil(t, img.SetLabel(seed.labelKey, seed.labelVal))
	h.AssertNil(t, img.SetEnv(seed.envKey, seed.envVal))
	h.AssertNil(t, img.SetEntrypoint("some", "entrypoint"))
	h.AssertNil(t, img.SetCmd("some", "cmd"))
	h.AssertNil(t, img.SetWorkingDir(seed.workingDir))
	h.AssertNil(t, img.Save())
	if imageType == localImg {
		dockerClient := h.DockerCli(t)
		h.PushImage(t, dockerClient, ref)
	}
	return ref
}

func initImage(t *testing.T, ref string, imageType ImageType, baseImg string) imgutil.Image {
	t.Helper()
	if imageType == remoteImg {
		img, err := remote.NewImage(ref, authn.DefaultKeychain, remote.FromBaseImage(baseImg))
		h.AssertNil(t, err)
		return img
	}
	dockerClient := h.DockerCli(t)
	img, err := local.NewImage(ref, dockerClient, local.FromBaseImage(baseImg))
	t.Cleanup(func() { h.DockerRmi(dockerClient, ref) })
	h.AssertNil(t, err)
	return img
}

func compare(t *testing.T, img1, img2 string) {
	t.Helper()

	ref1, err := name.ParseReference(img1, name.WeakValidation)
	h.AssertNil(t, err)

	ref2, err := name.ParseReference(img2, name.WeakValidation)
	h.AssertNil(t, err)

	auth1, err := authn.DefaultKeychain.Resolve(ref1.Context().Registry)
	h.AssertNil(t, err)

	auth2, err := authn.DefaultKeychain.Resolve(ref2.Context().Registry)
	h.AssertNil(t, err)

	v1img1, err := ggcrremote.Image(ref1, ggcrremote.WithAuth(auth1))
	h.AssertNil(t, err)

	v1img2, err := ggcrremote.Image(ref2, ggcrremote.WithAuth(auth2))
	h.AssertNil(t, err)

	cfg1, err := v1img1.ConfigFile()
	h.AssertNil(t, err)

	cfg2, err := v1img2.ConfigFile()
	h.AssertNil(t, err)

	h.AssertEq(t, cfg1, cfg2)

	h.AssertEq(t, ref1.Identifier(), ref2.Identifier())
}
