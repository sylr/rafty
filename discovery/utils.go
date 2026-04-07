// Package discovery contains utility functions for discovery.
package discovery

import "sylr.dev/rafty"

func ServersDiffer(source, target []rafty.Server) bool {
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
