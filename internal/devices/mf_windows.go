//go:build windows

package devices

import (
	"fmt"

	"patmonitor/internal/mf"
)

// ListCameras lists the webcams by asking Media Foundation.
func ListCameras() ([]Device, error) {
	list, err := mf.ListVideoDevices()
	if err != nil {
		return nil, fmt.Errorf("camera enumeration: %w", err)
	}
	out := make([]Device, 0, len(list))
	for _, d := range list {
		out = append(out, New(d.Name, d.Link))
	}
	return out, nil
}
