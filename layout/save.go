package layout

import (
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/pkg/errors"

	"github.com/buildpacks/imgutil"
)

func (i *Image) Save(additionalNames ...string) error {
	return i.SaveAs(i.Name(), additionalNames...)
}

// SaveAs ignores the image `Name()` method and saves the image according to name & additional names provided to this method
func (i *Image) SaveAs(name string, additionalNames ...string) error {
	err := i.mutateCreatedAt(i.Image, v1.Time{Time: i.createdAt})
	if err != nil {
		return errors.Wrap(err, "set creation time")
	}

	cfg, err := i.Image.ConfigFile()
	if err != nil {
		return errors.Wrap(err, "get image config")
	}
	cfg = cfg.DeepCopy()

	layers, err := i.Image.Layers()
	if err != nil {
		return errors.Wrap(err, "get image layers")
	}
	cfg.History = make([]v1.History, len(layers))
	for j := range cfg.History {
		cfg.History[j] = v1.History{
			Created: v1.Time{Time: i.createdAt},
		}
	}

	cfg.DockerVersion = ""
	cfg.Container = ""
	err = i.mutateConfigFile(i.Image, cfg)
	if err != nil {
		return errors.Wrap(err, "zeroing history")
	}

	var diagnostics []imgutil.SaveDiagnostic
	annotations := ImageRefAnnotation(i.refName)
	pathsToSave := append([]string{name}, additionalNames...)
	for _, path := range pathsToSave {
		// initialize image path
		path, err := Write(path, empty.Index)
		if err != nil {
			return err
		}

		err = path.AppendImage(i.Image, WithAnnotations(annotations))
		if err != nil {
			diagnostics = append(diagnostics, imgutil.SaveDiagnostic{ImageName: i.Name(), Cause: err})
		}
	}

	if len(diagnostics) > 0 {
		return imgutil.SaveError{Errors: diagnostics}
	}

	return nil
}

// mutateCreatedAt mutates the provided v1.Image to have the provided v1.Time and wraps the result
// into a layout.Image (requires for override methods like Layers()
func (i *Image) mutateCreatedAt(base v1.Image, created v1.Time) error { // FIXME: this function doesn't need arguments; we should also probably do this mutation at the time of image instantiation instead of at the point of saving
	image, err := mutate.CreatedAt(i.Image, v1.Time{Time: i.createdAt})
	if err != nil {
		return err
	}
	return i.setUnderlyingImage(image)
}
