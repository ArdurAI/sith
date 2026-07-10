// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"

	"sigs.k8s.io/yaml"
)

func writeYAML(output io.Writer, value any, label string) error {
	payload, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s YAML: %w", label, err)
	}
	if _, err := output.Write(payload); err != nil {
		return fmt.Errorf("write %s YAML: %w", label, err)
	}
	return nil
}
