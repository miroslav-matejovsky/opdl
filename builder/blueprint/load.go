package blueprint

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"

	topology "github.com/miroslav-matejovsky/opdl/builder/topology"
)

// topologyFile is the decode target for the merged blueprint HCL. Exactly one
// project block is expected across all files in a blueprint directory.
type topologyFile struct {
	Projects []topology.Project `hcl:"project,block"`
}

// Load reads every *.hcl file in dir, merges them, decodes the single project
// they describe, and validates it as topology. A returned Project is guaranteed
// well-formed. It is an error for a directory to hold zero or more than one
// project block.
func Load(dir string) (*topology.Project, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.hcl"))
	if err != nil {
		return nil, fmt.Errorf("scan blueprint dir %s: %w", dir, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no .hcl files found in blueprint dir %s", dir)
	}
	sort.Strings(matches)

	parser := hclparse.NewParser()
	files := make([]*hcl.File, 0, len(matches))
	for _, path := range matches {
		file, diags := parser.ParseHCLFile(path)
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse %s: %s", path, diags.Error())
		}
		files = append(files, file)
	}

	body := hcl.MergeFiles(files)
	var tf topologyFile
	if diags := gohcl.DecodeBody(body, nil, &tf); diags.HasErrors() {
		return nil, fmt.Errorf("decode blueprint in %s: %s", dir, diags.Error())
	}

	if len(tf.Projects) != 1 {
		return nil, fmt.Errorf("blueprint in %s must define exactly one project, found %d", dir, len(tf.Projects))
	}

	project := &tf.Projects[0]
	if err := project.Validate(); err != nil {
		return nil, fmt.Errorf("invalid blueprint in %s: %w", dir, err)
	}
	return project, nil
}
