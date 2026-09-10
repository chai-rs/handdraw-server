package model

const (
	// BoardIDPrefix identifies board metadata resources; the codec adds the underscore.
	BoardIDPrefix = "brd"
	// ProjectIDPrefix identifies project resources within the board domain.
	ProjectIDPrefix = "prj"
	// WorkspaceIDPrefix identifies the workspace reference accepted by board metadata.
	WorkspaceIDPrefix = "ws"
	// UserIDPrefix identifies a stable profile reference rather than a Supabase Auth UUID.
	UserIDPrefix = "usr"
)
