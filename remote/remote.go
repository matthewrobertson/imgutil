package remote

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/google/go-containerregistry/pkg/v1/validate"
	"github.com/pkg/errors"

	"github.com/buildpacks/imgutil"
)

const maxRetries = 2

type Image struct {
	keychain            authn.Keychain
	repoName            string
	image               v1.Image
	prevLayers          []v1.Layer
	createdAt           time.Time
	addEmptyLayerOnSave bool
	registrySettings    map[string]registrySetting
	requestedMediaTypes imgutil.MediaTypes
}

type registrySetting struct {
	insecure           bool
	insecureSkipVerify bool
}

// getters

func (i *Image) Architecture() (string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return "", errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return "", fmt.Errorf("missing config for image %q", i.repoName)
	}
	if cfg.Architecture == "" {
		return "", fmt.Errorf("missing Architecture for image %q", i.repoName)
	}
	return cfg.Architecture, nil
}

func (i *Image) CreatedAt() (time.Time, error) {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return time.Time{}, errors.Wrapf(err, "getting createdAt time for image %q", i.repoName)
	}
	return configFile.Created.UTC(), nil
}

func (i *Image) Entrypoint() ([]string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return nil, errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return nil, fmt.Errorf("missing config for image %q", i.repoName)
	}
	return cfg.Config.Entrypoint, nil
}

func (i *Image) Env(key string) (string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return "", errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return "", fmt.Errorf("missing config for image %q", i.repoName)
	}
	for _, envVar := range cfg.Config.Env {
		parts := strings.Split(envVar, "=")
		if parts[0] == key {
			return parts[1], nil
		}
	}
	return "", nil
}

func (i *Image) Found() bool {
	_, err := i.found()

	return err == nil
}

func (i *Image) found() (*v1.Descriptor, error) {
	reg := getRegistry(i.repoName, i.registrySettings)
	ref, auth, err := referenceForRepoName(i.keychain, i.repoName, reg.insecure)
	if err != nil {
		return nil, err
	}
	return remote.Head(ref, remote.WithAuth(auth), remote.WithTransport(http.DefaultTransport))
}

func (i *Image) Valid() bool {
	return i.valid() == nil
}

func (i *Image) valid() error {
	reg := getRegistry(i.repoName, i.registrySettings)
	ref, auth, err := referenceForRepoName(i.keychain, i.repoName, reg.insecure)
	if err != nil {
		return err
	}
	desc, err := remote.Get(ref, remote.WithAuth(auth), remote.WithTransport(http.DefaultTransport))
	if err != nil {
		return err
	}

	if desc.MediaType == types.OCIImageIndex || desc.MediaType == types.DockerManifestList {
		index, err := desc.ImageIndex()
		if err != nil {
			return err
		}
		return validate.Index(index, validate.Fast)
	}
	img, err := desc.Image()
	if err != nil {
		return err
	}
	return validate.Image(img, validate.Fast)
}

func (i *Image) GetAnnotateRefName() (string, error) {
	// TODO issue https://github.com/buildpacks/imgutil/issues/178
	return "", errors.New("not yet implemented")
}

func (i *Image) GetLayer(sha string) (io.ReadCloser, error) {
	layers, err := i.image.Layers()
	if err != nil {
		return nil, err
	}

	layer, err := findLayerWithSha(layers, sha)
	if err != nil {
		return nil, err
	}

	return layer.Uncompressed()
}

func (i *Image) Identifier() (imgutil.Identifier, error) {
	ref, err := name.ParseReference(i.repoName, name.WeakValidation)
	if err != nil {
		return nil, errors.Wrapf(err, "parsing reference for image %q", i.repoName)
	}

	hash, err := i.image.Digest()
	if err != nil {
		return nil, errors.Wrapf(err, "getting digest for image %q", i.repoName)
	}

	digestRef, err := name.NewDigest(fmt.Sprintf("%s@%s", ref.Context().Name(), hash.String()), name.WeakValidation)
	if err != nil {
		return nil, errors.Wrap(err, "creating digest reference")
	}

	return DigestIdentifier{
		Digest: digestRef,
	}, nil
}

func (i *Image) Label(key string) (string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return "", errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return "", fmt.Errorf("missing config for image %q", i.repoName)
	}
	labels := cfg.Config.Labels
	return labels[key], nil
}

func (i *Image) Labels() (map[string]string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return nil, errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return nil, fmt.Errorf("missing config for image %q", i.repoName)
	}
	return cfg.Config.Labels, nil
}

func (i *Image) ManifestSize() (int64, error) {
	return i.image.Size()
}

func (i *Image) Name() string {
	return i.repoName
}

func (i *Image) OS() (string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return "", errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return "", fmt.Errorf("missing config for image %q", i.repoName)
	}
	if cfg.OS == "" {
		return "", fmt.Errorf("missing OS for image %q", i.repoName)
	}
	return cfg.OS, nil
}

func (i *Image) OSVersion() (string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return "", errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return "", fmt.Errorf("missing config for image %q", i.repoName)
	}
	return cfg.OSVersion, nil
}

func (i *Image) TopLayer() (string, error) {
	all, err := i.image.Layers()
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", fmt.Errorf("image %q has no layers", i.Name())
	}
	topLayer := all[len(all)-1]
	hex, err := topLayer.DiffID()
	if err != nil {
		return "", err
	}
	return hex.String(), nil
}

func (i *Image) Variant() (string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return "", errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return "", fmt.Errorf("missing config for image %q", i.repoName)
	}
	return cfg.Variant, nil // it's optional so we don't care whether it's ""
}

func (i *Image) WorkingDir() (string, error) {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return "", errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return "", fmt.Errorf("missing config for image %q", i.repoName)
	}
	return cfg.Config.WorkingDir, nil
}

// setters

func (i *Image) AnnotateRefName(refName string) error {
	// TODO issue https://github.com/buildpacks/imgutil/issues/178
	return errors.New("not yet implemented")
}

func (i *Image) Rename(name string) {
	i.repoName = name
}

func (i *Image) SetArchitecture(architecture string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	configFile.Architecture = architecture
	i.image, err = mutate.ConfigFile(i.image, configFile)
	return err
}

func (i *Image) SetCmd(cmd ...string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	config := *configFile.Config.DeepCopy()
	config.Cmd = cmd
	i.image, err = mutate.Config(i.image, config)
	return err
}

func (i *Image) SetEntrypoint(ep ...string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	config := *configFile.Config.DeepCopy()
	config.Entrypoint = ep
	i.image, err = mutate.Config(i.image, config)
	return err
}

func (i *Image) SetEnv(key, val string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	config := *configFile.Config.DeepCopy()
	ignoreCase := configFile.OS == "windows"
	for idx, e := range config.Env {
		parts := strings.Split(e, "=")
		foundKey := parts[0]
		searchKey := key
		if ignoreCase {
			foundKey = strings.ToUpper(foundKey)
			searchKey = strings.ToUpper(searchKey)
		}
		if foundKey == searchKey {
			config.Env[idx] = fmt.Sprintf("%s=%s", key, val)
			i.image, err = mutate.Config(i.image, config)
			return err
		}
	}
	config.Env = append(config.Env, fmt.Sprintf("%s=%s", key, val))
	i.image, err = mutate.Config(i.image, config)
	return err
}

func (i *Image) SetLabel(key, val string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	config := *configFile.Config.DeepCopy()
	if config.Labels == nil {
		config.Labels = map[string]string{}
	}
	config.Labels[key] = val
	i.image, err = mutate.Config(i.image, config)
	return err
}

func (i *Image) SetOS(osVal string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	configFile.OS = osVal
	i.image, err = mutate.ConfigFile(i.image, configFile)
	return err
}

func (i *Image) SetOSVersion(osVersion string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	configFile.OSVersion = osVersion
	i.image, err = mutate.ConfigFile(i.image, configFile)
	return err
}

func (i *Image) SetVariant(variant string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	configFile.Variant = variant
	i.image, err = mutate.ConfigFile(i.image, configFile)
	return err
}

func (i *Image) SetWorkingDir(dir string) error {
	configFile, err := i.image.ConfigFile()
	if err != nil {
		return err
	}
	config := *configFile.Config.DeepCopy()
	config.WorkingDir = dir
	i.image, err = mutate.Config(i.image, config)
	return err
}

// modifiers

func (i *Image) AddLayer(path string) error {
	layer, err := tarball.LayerFromFile(path)
	if err != nil {
		return err
	}
	additions := layersAddendum([]v1.Layer{layer}, i.requestedMediaTypes.LayerType())
	i.image, err = mutate.Append(i.image, additions...)
	if err != nil {
		return errors.Wrap(err, "add layer")
	}
	return nil
}

// layersAddendum creates an Addendum array with the given layers
// and the desired media type
func layersAddendum(layers []v1.Layer, mediaType types.MediaType) []mutate.Addendum {
	additions := make([]mutate.Addendum, 0)
	for _, layer := range layers {
		additions = append(additions, mutate.Addendum{
			MediaType: mediaType,
			Layer:     layer,
		})
	}
	return additions
}

func (i *Image) AddLayerWithDiffID(path, diffID string) error {
	// this is equivalent to AddLayer in the remote case
	// it exists to provide optimize performance for local images
	return i.AddLayer(path)
}

func (i *Image) Delete() error {
	id, err := i.Identifier()
	if err != nil {
		return err
	}
	reg := getRegistry(i.repoName, i.registrySettings)
	ref, auth, err := referenceForRepoName(i.keychain, id.String(), reg.insecure)
	if err != nil {
		return err
	}
	return remote.Delete(ref, remote.WithAuth(auth))
}

func (i *Image) Rebase(baseTopLayer string, newBase imgutil.Image) error {
	newBaseRemote, ok := newBase.(*Image)
	if !ok {
		return errors.New("expected new base to be a remote image")
	}

	newImage, err := mutate.Rebase(i.image, &subImage{img: i.image, topDiffID: baseTopLayer}, newBaseRemote.image)
	if err != nil {
		return errors.Wrap(err, "rebase")
	}

	newImageConfig, err := newImage.ConfigFile()
	if err != nil {
		return err
	}

	newBaseRemoteConfig, err := newBaseRemote.image.ConfigFile()
	if err != nil {
		return err
	}

	newImageConfig.Architecture = newBaseRemoteConfig.Architecture
	newImageConfig.OS = newBaseRemoteConfig.OS
	newImageConfig.OSVersion = newBaseRemoteConfig.OSVersion

	newImage, err = mutate.ConfigFile(newImage, newImageConfig)
	if err != nil {
		return err
	}

	i.image = newImage
	return nil
}

func (i *Image) RemoveLabel(key string) error {
	cfg, err := i.image.ConfigFile()
	if err != nil {
		return errors.Wrapf(err, "getting config file for image %q", i.repoName)
	}
	if cfg == nil {
		return fmt.Errorf("missing config for image %q", i.repoName)
	}
	config := *cfg.Config.DeepCopy()
	delete(config.Labels, key)
	i.image, err = mutate.Config(i.image, config)
	return err
}

func (i *Image) ReuseLayer(sha string) error {
	layer, err := findLayerWithSha(i.prevLayers, sha)
	if err != nil {
		return err
	}
	i.image, err = mutate.AppendLayers(i.image, layer)
	return err
}

// extras

func (i *Image) CheckReadAccess() bool {
	_, err := i.found()
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			return transportErr.StatusCode != http.StatusUnauthorized &&
				transportErr.StatusCode != http.StatusForbidden
		}
		return false
	}
	return true
}

func (i *Image) CheckReadWriteAccess() bool {
	reg := getRegistry(i.repoName, i.registrySettings)
	ref, _, err := referenceForRepoName(i.keychain, i.repoName, reg.insecure)
	if err != nil {
		return false
	}
	return i.CheckReadAccess() && remote.CheckPushPermission(ref, i.keychain, http.DefaultTransport) == nil
}

// UnderlyingImage exposes the underlying image for testing
func (i *Image) UnderlyingImage() v1.Image {
	return i.image
}

// helpers

func findLayerWithSha(layers []v1.Layer, diffID string) (v1.Layer, error) {
	for _, layer := range layers {
		dID, err := layer.DiffID()
		if err != nil {
			return nil, errors.Wrap(err, "get diff ID for previous image layer")
		}
		if diffID == dID.String() {
			return layer, nil
		}
	}
	return nil, fmt.Errorf("previous image did not have layer with diff id %q", diffID)
}

// for rebase

type subImage struct {
	img       v1.Image
	topDiffID string
}

func (si *subImage) Layers() ([]v1.Layer, error) {
	all, err := si.img.Layers()
	if err != nil {
		return nil, err
	}
	for i, l := range all {
		d, err := l.DiffID()
		if err != nil {
			return nil, err
		}
		if d.String() == si.topDiffID {
			return all[0 : i+1], nil
		}
	}
	return nil, errors.New("could not find base layer in image")
}
func (si *subImage) ConfigFile() (*v1.ConfigFile, error)     { return si.img.ConfigFile() }
func (si *subImage) BlobSet() (map[v1.Hash]struct{}, error)  { panic("Not Implemented") }
func (si *subImage) MediaType() (types.MediaType, error)     { panic("Not Implemented") }
func (si *subImage) ConfigName() (v1.Hash, error)            { panic("Not Implemented") }
func (si *subImage) RawConfigFile() ([]byte, error)          { panic("Not Implemented") }
func (si *subImage) Digest() (v1.Hash, error)                { panic("Not Implemented") }
func (si *subImage) Manifest() (*v1.Manifest, error)         { panic("Not Implemented") }
func (si *subImage) RawManifest() ([]byte, error)            { panic("Not Implemented") }
func (si *subImage) LayerByDigest(v1.Hash) (v1.Layer, error) { panic("Not Implemented") }
func (si *subImage) LayerByDiffID(v1.Hash) (v1.Layer, error) { panic("Not Implemented") }
func (si *subImage) Size() (int64, error)                    { panic("Not Implemented") }
