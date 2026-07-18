export interface AuthRecord {
  id: string;
  email: string;
  verified?: boolean;
  first_name: string;
  last_name: string;
  phone: string;
  avatar_file_id?: string;
  created: string;
  updated: string;
}

export interface AuthResponse {
  token: string;
  record: AuthRecord;
}

export interface CollectionField {
  name: string;
  type: "text" | "number" | "bool" | "date" | "json";
  required?: boolean;
}

export interface CollectionSchema {
  fields: CollectionField[];
}

export type RuleKind = "public" | "authenticated" | "owner";

export interface CollectionRules {
  list?: RuleKind;
  view?: RuleKind;
  create?: RuleKind;
  update?: RuleKind;
  delete?: RuleKind;
}

export interface Collection {
  id: string;
  name: string;
  schema: CollectionSchema;
  rules: CollectionRules;
  record_count: number;
  created: string;
  updated: string;
}

// RecordBase is what every dynamic collection record has in common;
// extend it with your own fields for typed records(), e.g.:
//   interface Post extends RecordBase { title: string; published: boolean }
export interface RecordBase {
  id: string;
  owner_id?: string;
  created: string;
  updated: string;
  [key: string]: unknown;
}

export interface ListParams {
  filter?: string;
  sort?: "created" | "-created";
  limit?: number;
  cursor?: string;
}

export interface ListResponse<T> {
  items: T[];
  nextCursor: string;
}

export interface FileRecord {
  id: string;
  owner_id?: string;
  filename: string;
  size: number;
  mime: string;
  created: string;
}

export interface RAGSource {
  id: string;
  owner_id?: string;
  file_id: string;
  filename: string;
  status: "pending" | "processing" | "done" | "error";
  chunk_count: number;
  error?: string;
  created: string;
  updated: string;
}

export interface RAGScoredChunk {
  source_id: string;
  text: string;
  score: number;
}

export interface RAGAnswer {
  answer: string;
  sources: RAGScoredChunk[];
}

export interface FileListResponse extends ListResponse<FileRecord> {
  total: number;
}

export interface RAGSourceListResponse extends ListResponse<RAGSource> {
  total: number;
  status_counts: Record<string, number>;
}

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ChatResponse {
  content: string;
  cached: boolean;
}

export interface UsageRecord {
  id: string;
  user_id?: string;
  provider: string;
  model: string;
  tokens_in: number;
  tokens_out: number;
  cost_estimate: number;
  cached: boolean;
  created: string;
}

export interface RealtimeEvent<T = RecordBase> {
  action: "create" | "update" | "delete";
  collection: string;
  record: T;
}

export interface UpdateProfileParams {
  email: string;
  first_name?: string;
  last_name?: string;
  phone?: string;
}

export interface CreatePasswordResetResponse {
  token: string;
  expires_at: string;
  user_id: string;
  email: string;
}
