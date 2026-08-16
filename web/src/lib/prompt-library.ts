export interface PromptTemplate {
  id: string
  label: string
  prompt: string
}

// Starter prompts shown in the composer's "/" menu and the empty-chat
// screen. Deliberately plain strings, not tool calls — the model decides
// whether/which tool to use for each, same as if the admin typed it.
export const PROMPT_LIBRARY: PromptTemplate[] = [
  { id: "analyze-data", label: "Analyze my data", prompt: "Look at my collections and summarize what data I have and how it's structured." },
  { id: "summarize-files", label: "Summarize uploaded files", prompt: "Summarize the files I've uploaded recently." },
  { id: "create-collection", label: "Create a collection", prompt: "Help me create a new collection. Ask what it's for if it's not obvious." },
  { id: "generate-schema", label: "Generate schema", prompt: "Design a schema for a collection based on what I describe next." },
  { id: "find-duplicates", label: "Find duplicates", prompt: "Help me think through how to find duplicate records in a collection." },
  { id: "generate-report", label: "Generate a report", prompt: "Generate a short report summarizing the current state of my workspace." },
  { id: "explain-record", label: "Explain this record", prompt: "Explain what this record represents and whether anything looks off." },
  { id: "search-knowledge", label: "Search my knowledge", prompt: "Search across my collections and files for information about: " },
]
