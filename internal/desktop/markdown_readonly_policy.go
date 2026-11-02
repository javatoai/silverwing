package desktop

// Generated and unrecognised Markdown hosts remain read-only. Materials and
// global/group instructions grant editing through their own read capabilities.
const markdownReadingOnly = true

const taskMaterialsMarkdownEditContext = "task-materials"

func markdownSaveRefWritable(ref documentSaveRef) bool {
	return !ref.readOnly && (ref.kind == "material-text" || ref.kind == "material-markdown" ||
		ref.kind == "instruction" && (ref.scope == "global" || ref.scope == "group" || ref.scope == "task" && ref.localTaskRules))
}
