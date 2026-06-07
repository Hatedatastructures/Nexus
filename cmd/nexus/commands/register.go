package commands

// RegisterAllCommands registers all CLI commands.
// Call this once at program startup before using GetCommand.
func RegisterAllCommands() {
	Register(&BackupCommand{})
	Register(&ChatCommand{})
	Register(&ConfigCommand{})
	Register(&CronCommand{})
	Register(&DoctorCommand{})
	Register(&ExportCommand{})
	Register(&GatewayCommand{})
	Register(&ImportCommand{})
	Register(&LogsCommand{})
	Register(&MCPServeCommand{})
	Register(&MemoryCommand{})
	Register(&ModelCommand{})
	Register(&ProviderCommand{})
	Register(&RLCommand{})
	Register(&SessionCommand{})
	Register(&SetupCommand{})
	Register(&SkillCommand{})
	Register(&StatusCommand{})
	Register(&ToolCommand{})
	Register(&VersionCommand{})
}
