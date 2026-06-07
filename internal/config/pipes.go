package config

// UserServerName is the reserved virtual server name for user-defined pipes.
// No real upstream may be named "user".
const UserServerName = "user"

// IsReservedServerName returns true if the name conflicts with a built-in virtual server.
func IsReservedServerName(name string) bool {
	return name == UserServerName
}

// PipeConfig defines a multi-step tool sequence that mini executes as a single callable tool.
type PipeConfig struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Inputs      map[string]InputSchema `yaml:"inputs,omitempty"`
	Steps       []StepConfig           `yaml:"steps"`
	Output      map[string]string      `yaml:"output,omitempty"`
	Permission  string                 `yaml:"permission,omitempty"`
	ShowSteps   bool                   `yaml:"show_steps,omitempty"`
}

// InputSchema declares one named input parameter for a pipe.
type InputSchema struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Description string `yaml:"description,omitempty"`
	Default     any    `yaml:"default,omitempty"`
}

// StepConfig defines a single step in a pipe execution sequence.
type StepConfig struct {
	ID              string            `yaml:"id"`
	Server          string            `yaml:"server,omitempty"`
	Tool            string            `yaml:"tool,omitempty"`
	Args            map[string]any    `yaml:"args,omitempty"`
	If              string            `yaml:"if,omitempty"`
	Set             map[string]string `yaml:"set,omitempty"`
	Silent          bool              `yaml:"silent,omitempty"`
	ContinueOnError bool              `yaml:"continue_on_error,omitempty"`
	Timeout         string            `yaml:"timeout,omitempty"`
}
