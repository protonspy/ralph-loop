package graph

// The living-documentation ontology (ARCHITECTURE §7.3). Node kinds and relation
// types are plain strings in the store; these constants are the sanctioned
// vocabulary so every writer (projector, MCP tools, brain prompts) agrees.

// Node kinds.
const (
	KindProgram     = "program"
	KindFeat        = "feat"
	KindSpec        = "spec"
	KindRequirement = "requirement"
	KindComponent   = "component"
	KindFile        = "file"
	KindDecision    = "decision"
	KindTest        = "test"
	KindAgent       = "agent"
	KindSkill       = "skill"
	KindConcept     = "concept"
)

// Relation types.
const (
	RelDependsOn    = "DEPENDS_ON"    // feat→feat, component→component
	RelHasFeat      = "HAS_FEAT"      // program→feat (rl extension: keeps the graph connected)
	RelHasReq       = "HAS_REQ"       // spec→requirement
	RelHasComponent = "HAS_COMPONENT" // feat→component
	RelTracesTo     = "TRACES_TO"     // task→requirement
	RelImplements   = "IMPLEMENTS"    // file→component
	RelVerifies     = "VERIFIES"      // test→requirement
	RelJustifies    = "JUSTIFIES"     // decision→component|feat
	RelWorkedOn     = "WORKED_ON"     // agent→component
	RelRelatesTo    = "RELATES_TO"    // concept→concept
	RelHasStatus    = "HAS_STATUS"    // feat→feat self-status (rl extension: temporal status facts)
)
