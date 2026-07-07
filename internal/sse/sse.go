// Copyright (C) 2026 Joey Kot <joey.kot.x@gmail.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed WITHOUT ANY WARRANTY; without even the
// implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
// See <https://www.gnu.org/licenses/> for more details.

package sse

import (
	"encoding/json"
	"fmt"
	"io"
)

func Data(w io.Writer, data any) error {
	switch v := data.(type) {
	case string:
		_, err := fmt.Fprintf(w, "data: %s\n\n", v)
		return err
	default:
		encoded, err := json.Marshal(data)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "data: %s\n\n", encoded)
		return err
	}
}
