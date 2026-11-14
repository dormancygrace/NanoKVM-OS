package storage

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"NanoKVM-Server/remotemedia"
)

// Medium describes what is in the virtual drive, for the services that are
// not the web page (Redfish).
type Medium struct {
	Inserted bool
	// Source: "local" (an image of /data), "browser", "http", "smb" or
	// "netboot".
	Source string
	// Image is where the medium comes from, without credentials or a query:
	// the path of a local image, the URL of a remote one, the file name of
	// a browser's.
	Image string
	CDROM bool
}

// ErrDriveInUse is what errors.Is finds in a refusal that means another
// medium holds the drive: the request is fine and can be repeated after an
// eject.
var ErrDriveInUse = errors.New("the virtual drive is in use")

// driveInUse is such a refusal, in the words the page shows.
type driveInUse string

func (e driveInUse) Error() string        { return string(e) }
func (e driveInUse) Is(target error) bool { return target == ErrDriveInUse }

// ErrNetbootMedium: network boot keeps its image in the drive; it is turned
// off on its own page.
var ErrNetbootMedium = errors.New("Turn off network boot first")

// Medium reports the drive's medium. A remote image still connecting holds
// the drive without being in it.
func (s *Service) Medium() (Medium, error) {
	s.mu.Lock()
	r, netboot := s.remote, s.netboot != nil
	var remote Medium
	if r != nil {
		remote = Medium{Inserted: r.mounted, Source: r.kind, Image: r.location, CDROM: true}
		if remote.Image == "" {
			remote.Image = r.name
		}
	}
	s.mu.Unlock()
	if r != nil {
		return remote, nil
	}
	image, cdrom, err := s.MountedImage()
	if err != nil || image == "" {
		return Medium{}, err
	}
	if netboot {
		return Medium{Inserted: true, Source: "netboot", Image: image, CDROM: cdrom}, nil
	}
	return Medium{Inserted: true, Source: "local", Image: image, CDROM: cdrom}, nil
}

// InsertURL puts the image at an http, https or smb URL into the drive, as
// the page's "From URL" does, and waits until the computer has it or ctx
// ends: the caller learns why an image did not connect. The session then
// belongs to the device and lasts until an eject.
func (s *Service) InsertURL(ctx context.Context, rawURL, username, password string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("The image address is not a URL")
	}
	// What is shown later: no credentials, no (possibly signed) query.
	shown := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	var r *remoteSession
	if strings.EqualFold(u.Scheme, "smb") {
		options := remotemedia.SMBOptions{URL: rawURL, Username: username, Password: password}
		if err := remotemedia.ValidateSMBOptions(options); err != nil {
			return err
		}
		r, err = s.startRemote("smb", shown.String(), func(ctx context.Context) (remotemedia.Source, string, uint64, error) {
			source, err := remotemedia.OpenSMB(ctx, options)
			if err != nil {
				return nil, "", 0, err
			}
			return source, source.Name, source.Size, nil
		})
	} else {
		options := remotemedia.HTTPOptions{URL: rawURL, Username: username, Password: password}
		if err := remotemedia.ValidateHTTPOptions(options); err != nil {
			return err
		}
		r, err = s.startRemote("http", shown.String(), func(ctx context.Context) (remotemedia.Source, string, uint64, error) {
			source, err := remotemedia.OpenHTTP(ctx, options)
			if err != nil {
				return nil, "", 0, err
			}
			return source, source.Name, source.Size, nil
		})
	}
	if err != nil {
		return err
	}
	select {
	case err := <-r.ready:
		return err
	case <-ctx.Done():
		r.close()
		<-r.released
		return errors.New("The image did not connect in time")
	}
}

// EjectMedium takes the medium out, whatever put it in: a local image
// (forced, as a drive's button), or a remote one, whose session ends.
// Network boot keeps its image.
func (s *Service) EjectMedium() error {
	s.mu.Lock()
	r, netboot := s.remote, s.netboot != nil
	s.mu.Unlock()
	if r != nil {
		r.close()
		select {
		case <-r.released:
		case <-time.After(10 * time.Second):
			return errors.New("The remote image did not disconnect in time")
		}
		return nil
	}
	if netboot {
		return ErrNetbootMedium
	}
	_, err := s.mountImage("", false, true)
	return err
}
