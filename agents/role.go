package agents

// Role defines what kind of agent this is.
// We use a custom string type instead of plain string for one reason:
// the Go compiler will stop you from accidentally passing the wrong thing.
//
// For example, this will NOT compile:
//
//	agent.Role = "spelunker"   // typo — compiler catches it immediately
//
// But this will:
//
//	agent.Role = Planner       // correct — uses the named constant
type Role string

const (
	// Planner breaks the incoming task into smaller, ordered steps.
	// It is always the first agent to run in a crew.
	// The planner does not write final output — it writes a plan.
	Planner Role = "planner"

	// Writer takes the plan from the planner and does the actual work.
	// It is the workhorse — it searches, reads, processes, and produces output.
	Writer Role = "writer"

	// Critic reads the writer's output and reviews it.
	// It checks for quality, accuracy, and completeness.
	// Its feedback is appended to the final result.
	Critic Role = "critic"

	// Executor is a general-purpose agent for running actions —
	// calling APIs, running scripts, interacting with external systems.
	// Use it when you need a doer rather than a thinker or writer.
	Executor Role = "executor"

	// Researcher is a focused reading and summarising agent.
	// Unlike the Writer, it does not produce long-form output.
	// It collects facts and passes them to the Writer or Planner.
	Researcher Role = "researcher"
)

// String returns the string value of the role.
// This satisfies the fmt.Stringer interface, so when you print a Role
// it shows the human-readable name instead of just the type.
//
// Example:
//
//	fmt.Println(Planner)  →  "planner"
func (r Role) String() string {
	return string(r)
}

// IsValid reports whether the role is one of the known built-in roles.
// The runtime calls this when loading agents.yaml to catch typos early —
// before any goroutine starts, before any LLM is called.
//
// Example:
//
//	Role("spelunker").IsValid()  →  false
//	Planner.IsValid()            →  true
func (r Role) IsValid() bool {
	switch r {
	case Planner, Writer, Critic, Executor, Researcher:
		return true
	}
	return false
}

// SystemPrompt returns the base system prompt for this role.
// Every agent gets this as its first instruction to the LLM.
// It tells the model what kind of worker it is before the task arrives.
//
// When you add a custom role in the future, you add a case here too.
func (r Role) SystemPrompt() string {
	switch r {
	case Planner:
		return "You are a planning agent. Your only job is to read the task " +
			"and break it down into a clear, numbered list of steps for other " +
			"agents to follow. Do not do the work yourself — only plan it. " +
			"Be specific and actionable."

	case Writer:
		return "You are a writing agent. You receive a plan and execute it " +
			"by researching, thinking, and producing well-structured written output. " +
			"Use your tools when you need information from the web or files. " +
			"Be thorough and cite your sources."

	case Critic:
		return "You are a critic agent. You receive a piece of work and review it " +
			"for quality, accuracy, completeness, and clarity. " +
			"Be constructive. Point out what is good, what is missing, " +
			"and what could be improved. Give a score out of 10."

	case Executor:
		return "You are an executor agent. You carry out specific actions " +
			"using the tools available to you. Follow instructions precisely. " +
			"Report back exactly what happened — success or failure."

	case Researcher:
		return "You are a research agent. Your job is to find, read, and " +
			"summarise information relevant to the task. " +
			"Do not produce long reports — produce concise, factual summaries " +
			"that other agents can use."

	default:
		return "You are a helpful AI agent. Complete the task given to you " +
			"as accurately and thoroughly as possible."
	}
}
