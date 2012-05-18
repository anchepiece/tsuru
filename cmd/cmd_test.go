package cmd

import (
	"bytes"
	"errors"
	"io"
	. "launchpad.net/gocheck"
	"testing"
)

func Test(t *testing.T) { TestingT(t) }

type S struct{}

var _ = Suite(&S{})
var manager Manager

func (s *S) SetUpTest(c *C) {
	var stdout, stderr bytes.Buffer
	manager = NewManager(&stdout, &stderr)
}

type TestCommand struct{}

func (c *TestCommand) Info() *Info {
	return &Info{
		Name:  "foo",
		Desc:  "Foo do anything or nothing.",
		Usage: "glb foo",
	}
}

func (c *TestCommand) Run(context *Context, client Doer) error {
	io.WriteString(context.Stdout, "Running TestCommand")
	return nil
}

func (c *TestCommand) Subcommands() map[string]interface{} {
	return map[string]interface{}{
		"ble": &TestSubCommand{},
	}
}

type TestSubCommand struct{}

func (c *TestSubCommand) Info() *Info {
	return &Info{
		Name:  "ble",
		Desc:  "Ble do anything or nothing.",
		Usage: "glb foo ble",
	}
}

type ErrorCommand struct{}

func (c *ErrorCommand) Info() *Info {
	return &Info{Name: "error"}
}

func (c *ErrorCommand) Run(context *Context, client Doer) error {
	return errors.New("You are wrong")
}

func (s *S) TestRegister(c *C) {
	manager.Register(&TestCommand{})
	badCall := func() { manager.Register(&TestCommand{}) }
	c.Assert(badCall, PanicMatches, "command already registered: foo")
}

func (s *S) TestManagerRunShouldWriteErrorsOnStderr(c *C) {
	manager.Register(&ErrorCommand{})
	manager.Run([]string{"error"})
	c.Assert(manager.Stderr.(*bytes.Buffer).String(), Equals, "You are wrong")
}

func (s *S) TestRun(c *C) {
	manager.Register(&TestCommand{})
	manager.Run([]string{"foo"})
	c.Assert(manager.Stdout.(*bytes.Buffer).String(), Equals, "Running TestCommand")
}

func (s *S) TestRunCommandThatDoesNotExist(c *C) {
	manager.Run([]string{"bar"})
	c.Assert(manager.Stderr.(*bytes.Buffer).String(), Equals, "command bar does not exist\n")
}

type TicCmd struct {
	record *RecordCmd
}

func (c *TicCmd) Info() *Info {
	return &Info{Name: "tic"}
}

func (c *TicCmd) Subcommands() map[string]interface{} {
	c.record = &RecordCmd{}
	return map[string]interface{}{"tac": &TacCmd{}, "record": c.record}
}

type TacCmd struct{}

func (c *TacCmd) Info() *Info {
	return &Info{Name: "tac"}
}

func (c *TacCmd) Run(context *Context, client Doer) error {
	io.WriteString(context.Stdout, "Running tac subcommand")
	return nil
}

type RecordCmd struct {
	args []string
}

func (c *RecordCmd) Info() *Info {
	return &Info{Name: "record"}
}

func (c *RecordCmd) Run(context *Context, client Doer) error {
	c.args = context.Args
	return nil
}

func (s *S) TestSubcommand(c *C) {
	manager.Register(&TicCmd{})
	manager.Run([]string{"tic", "tac"})
	c.Assert(manager.Stdout.(*bytes.Buffer).String(), Equals, "Running tac subcommand")
}

func (s *S) TestSubcommandWithArgs(c *C) {
	expected := []string{"arg1", "arg2"}
	cmd := &TicCmd{}
	manager.Register(cmd)
	manager.Run([]string{"tic", "record", "arg1", "arg2"})
	c.Assert(cmd.record.args, DeepEquals, expected)
}

func (s *S) TestHelp(c *C) {
	expected := `Usage: glb command [args]
`
	context := Context{[]string{}, manager.Stdout, manager.Stderr}
	command := Help{}
	err := command.Run(&context, nil)
	c.Assert(err, IsNil)
	c.Assert(manager.Stdout.(*bytes.Buffer).String(), Equals, expected)
}

func (s *S) TestHelpCommandShouldBeRegisteredByDefault(c *C) {
	var stdout, stderr bytes.Buffer
	m := NewManager(&stdout, &stderr)
	_, exists := m.commands["help"]
	c.Assert(exists, Equals, true)
}

func (s *S) TestRunWithoutArgsShouldRunsHelp(c *C) {
	expected := `Usage: glb command [args]
`
	manager.Run([]string{})
	c.Assert(manager.Stdout.(*bytes.Buffer).String(), Equals, expected)
}

func (s *S) TestHelpShouldReturnsHelpForACmd(c *C) {
	expected := `Usage: glb foo

Foo do anything or nothing.
`
	manager.Register(&TestCommand{})

	context := Context{[]string{"foo"}, manager.Stdout, manager.Stderr}
	command := Help{manager: &manager}
	err := command.Run(&context, nil)
	c.Assert(err, IsNil)
	c.Assert(manager.Stdout.(*bytes.Buffer).String(), Equals, expected)
}

func (s *S) TestHelpShouldReturnsHelpForASubCmd(c *C) {
	expected := `Usage: glb foo ble

Ble do anything or nothing.
`
	manager.Register(&TestCommand{})

	context := Context{[]string{"foo", "ble"}, manager.Stdout, manager.Stderr}
	command := Help{manager: &manager}
	err := command.Run(&context, nil)
	c.Assert(err, IsNil)
	c.Assert(manager.Stdout.(*bytes.Buffer).String(), Equals, expected)
}
