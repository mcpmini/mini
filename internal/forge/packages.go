package forge

import (
	"fmt"
	"regexp"
)

const maxPackages = 8

var packageSpecifierPattern = regexp.MustCompile(`^(npm|jsr):[@a-zA-Z0-9][a-zA-Z0-9@/._^~+-]*$`)

func validatePackages(packages []string) error {
	if len(packages) > maxPackages {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf("too many packages: %d (max %d)", len(packages), maxPackages)}
	}
	for _, p := range packages {
		if !packageSpecifierPattern.MatchString(p) {
			return &Error{Kind: KindRunner, Message: fmt.Sprintf("invalid package specifier %q: only npm: and jsr: specifiers are allowed", p)}
		}
	}
	return nil
}
