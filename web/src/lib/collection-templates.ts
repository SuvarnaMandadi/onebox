import type { Field } from "@/lib/types"

export interface CollectionTemplate {
  id: string
  label: string
  description: string
  icon: string
  color: string
  fields: Field[]
}

// Field types are the exact set the schema engine accepts (see
// validFieldTypes in internal/server/collection_schema.go) — "text",
// "number", "bool", "date", "json". Kept in sync by hand here since these
// are illustrative starting points, not derived data; onebox's live
// capabilities are already fetched for the AI copilot (chatbot_capabilities.go)
// but that's a different concern from client-side template scaffolding.
export const COLLECTION_TEMPLATES: CollectionTemplate[] = [
  {
    id: "blank",
    label: "Blank",
    description: "Start from scratch — you'll add your own fields.",
    icon: "Database",
    color: "gray",
    fields: [],
  },
  {
    id: "tasks",
    label: "Tasks",
    description: "Title, status, due date, assignee.",
    icon: "Clipboard",
    color: "blue",
    fields: [
      { name: "title", type: "text", required: true },
      { name: "status", type: "text", required: true },
      { name: "due_date", type: "date", required: false },
      { name: "assignee", type: "text", required: false },
      { name: "done", type: "bool", required: false },
    ],
  },
  {
    id: "contacts",
    label: "Contacts",
    description: "Name, email, phone, company.",
    icon: "Users",
    color: "violet",
    fields: [
      { name: "name", type: "text", required: true },
      { name: "email", type: "text", required: true },
      { name: "phone", type: "text", required: false },
      { name: "company", type: "text", required: false },
    ],
  },
  {
    id: "blog_posts",
    label: "Blog Posts",
    description: "Title, body, published flag, tags.",
    icon: "FileText",
    color: "amber",
    fields: [
      { name: "title", type: "text", required: true },
      { name: "body", type: "text", required: true },
      { name: "published", type: "bool", required: false },
      { name: "tags", type: "json", required: false },
    ],
  },
  {
    id: "products",
    label: "Products",
    description: "Name, price, stock, description.",
    icon: "Package",
    color: "green",
    fields: [
      { name: "name", type: "text", required: true },
      { name: "price", type: "number", required: true },
      { name: "stock", type: "number", required: false },
      { name: "description", type: "text", required: false },
    ],
  },
  {
    id: "events",
    label: "Events",
    description: "Title, starts at, location, capacity.",
    icon: "Calendar",
    color: "pink",
    fields: [
      { name: "title", type: "text", required: true },
      { name: "starts_at", type: "date", required: true },
      { name: "location", type: "text", required: false },
      { name: "capacity", type: "number", required: false },
    ],
  },
]
