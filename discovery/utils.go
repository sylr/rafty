package discovery

import "github.com/hashicorp/raft"

func ServersDiffer(source, target []raft.Server) bool {
	if len(source) != len(target) {
		return true
	}

	for _, s := range source {
		found := false
		for _, t := range target {
			if s.ID == t.ID && s.Address == t.Address {
				found = true
			}
		}

		if !found {
			return true
		}
	}

	return false
}
