package fix

import (
	"testing"

	"gopkg.in/check.v1"
)

func Test(t *testing.T) { check.TestingT(t) }

type S struct{}

var _ = check.Suite(&S{})

func (s *S) TestGetImageDigest(c *check.C) {
	output := `
Pull output...
Digest: dockershouldhaveaeasywaytogetitfromimage
More pull output..
`
	digest, err := GetImageDigest(output)
	c.Assert(err, check.IsNil)
	c.Assert(digest, check.Equals, "@dockershouldhaveaeasywaytogetitfromimage")
}

func (s *S) TestGetImageDigestNoDigest(c *check.C) {
	output := `
Pull output...
No digest here
More pull output..
`
	_, err := GetImageDigest(output)
	c.Assert(err, check.NotNil)
}