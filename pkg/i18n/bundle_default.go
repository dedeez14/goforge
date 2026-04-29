package i18n

// DefaultBundle returns a Bundle pre-loaded with translations for
// every error code emitted by the framework's built-in modules
// (auth, validator, RBAC, menu, rate-limiter, request decoding).
// Apps may extend it via Add / AddMany before passing the bundle
// to server.New.
//
// Translations are intentionally short and direct — long sentences
// are awkward to render in toast notifications.
//
// Codes here must match the strings passed to errs.New / errs.NotFound
// / errs.Conflict / errs.Forbidden / errs.InvalidInput / errs.Unauthorized
// in the codebase. A mismatch would silently fall back to the English
// hard-coded message because Lookup misses; bundle_default_test.go
// scans the source tree to make that mistake hard to keep.
func DefaultBundle() *Bundle {
	b := NewBundle(LocaleEN)

	// Generic / transport-layer
	b.AddMany("internal", map[Locale]string{
		LocaleEN: "internal server error",
		LocaleID: "kesalahan internal pada server",
	})
	b.AddMany("validation", map[Locale]string{
		LocaleEN: "request validation failed",
		LocaleID: "data permintaan tidak valid",
	})
	b.AddMany("rate_limited", map[Locale]string{
		LocaleEN: "too many requests, slow down",
		LocaleID: "terlalu banyak permintaan, mohon perlambat",
	})

	// Request shape (server.go + handler-level)
	b.AddMany("request.malformed", map[Locale]string{
		LocaleEN: "malformed request body",
		LocaleID: "format isi permintaan tidak valid",
	})
	b.AddMany("request.invalid_uuid", map[Locale]string{
		LocaleEN: "path parameter is not a valid UUID",
		LocaleID: "parameter pada path bukan UUID yang valid",
	})
	b.AddMany("request.tenant_id_invalid", map[Locale]string{
		LocaleEN: "tenant_id is not a valid UUID",
		LocaleID: "tenant_id bukan UUID yang valid",
	})
	b.AddMany("request.too_large", map[Locale]string{
		LocaleEN: "request body too large",
		LocaleID: "isi permintaan terlalu besar",
	})
	b.AddMany("route.not_found", map[Locale]string{
		LocaleEN: "route not found",
		LocaleID: "rute tidak ditemukan",
	})
	b.AddMany("route.method_not_allowed", map[Locale]string{
		LocaleEN: "method not allowed",
		LocaleID: "metode HTTP tidak diizinkan",
	})

	// Auth (middleware + JWT issuer)
	b.AddMany("auth.missing_token", map[Locale]string{
		LocaleEN: "authentication required",
		LocaleID: "autentikasi diperlukan",
	})
	b.AddMany("auth.missing_user", map[Locale]string{
		LocaleEN: "authentication required",
		LocaleID: "autentikasi diperlukan",
	})
	// auth.invalid is emitted from the refresh path when the token
	// is unknown to the rotation store - i.e. revoked or already
	// consumed - NOT when it is expired (jwt.invalid covers that).
	// Matching the message to the actual cause keeps client retry
	// logic honest.
	b.AddMany("auth.invalid", map[Locale]string{
		LocaleEN: "invalid or revoked refresh token",
		LocaleID: "refresh token tidak valid atau telah dicabut",
	})
	b.AddMany("auth.invalid_subject", map[Locale]string{
		LocaleEN: "invalid token subject",
		LocaleID: "subjek token tidak valid",
	})
	// auth.wrong_token_kind is emitted when a *protected* endpoint is
	// hit with a non-access bearer (e.g. a refresh token). The
	// inverse case - hitting /refresh with an access token - is a
	// distinct auth.refresh_token_required so the translated
	// message tells the client what to do.
	b.AddMany("auth.wrong_token_kind", map[Locale]string{
		LocaleEN: "access token required",
		LocaleID: "diperlukan access token",
	})
	b.AddMany("auth.refresh_token_required", map[Locale]string{
		LocaleEN: "refresh token required",
		LocaleID: "diperlukan refresh token",
	})
	b.AddMany("auth.token_reused", map[Locale]string{
		LocaleEN: "refresh token has already been used",
		LocaleID: "refresh token sudah pernah digunakan",
	})
	b.AddMany("jwt.invalid", map[Locale]string{
		LocaleEN: "invalid or expired token",
		LocaleID: "token tidak valid atau telah kedaluwarsa",
	})

	// User / credentials
	b.AddMany("user.invalid_credentials", map[Locale]string{
		LocaleEN: "invalid email or password",
		LocaleID: "email atau kata sandi salah",
	})
	b.AddMany("user.email_taken", map[Locale]string{
		LocaleEN: "email already registered",
		LocaleID: "email sudah terdaftar",
	})
	b.AddMany("user.not_found", map[Locale]string{
		LocaleEN: "user not found",
		LocaleID: "pengguna tidak ditemukan",
	})

	// Tenant
	b.AddMany("tenant.missing", map[Locale]string{
		LocaleEN: "X-Tenant-ID header is required",
		LocaleID: "header X-Tenant-ID wajib disertakan",
	})
	b.AddMany("tenant.invalid", map[Locale]string{
		LocaleEN: "X-Tenant-ID header is not a valid UUID",
		LocaleID: "header X-Tenant-ID bukan UUID yang valid",
	})

	// Idempotency
	b.AddMany("idempotency.key_too_long", map[Locale]string{
		LocaleEN: "idempotency key is too long",
		LocaleID: "idempotency key terlalu panjang",
	})
	b.AddMany("idempotency.key_reused", map[Locale]string{
		LocaleEN: "idempotency key has already been used with a different request",
		LocaleID: "idempotency key sudah pernah digunakan untuk permintaan berbeda",
	})

	// RBAC — codes match internal/domain/rbac/rbac.go and
	// internal/usecase/rbac.go. Keep this section in sync with those
	// errs.New(...) call sites; bundle_default_test.go scans the tree
	// and fails if any used code is missing here.
	b.AddMany("rbac.permission_not_found", map[Locale]string{
		LocaleEN: "permission not found",
		LocaleID: "izin tidak ditemukan",
	})
	b.AddMany("rbac.permission_code_taken", map[Locale]string{
		LocaleEN: "permission code already exists",
		LocaleID: "kode izin sudah digunakan",
	})
	b.AddMany("rbac.permission_invalid", map[Locale]string{
		LocaleEN: "resource and action are required",
		LocaleID: "resource dan action wajib diisi",
	})
	b.AddMany("rbac.permission_required", map[Locale]string{
		LocaleEN: "missing required permission",
		LocaleID: "izin yang diperlukan belum diberikan",
	})
	b.AddMany("rbac.permission_code_required", map[Locale]string{
		LocaleEN: "permission code is required",
		LocaleID: "kode izin wajib diisi",
	})
	b.AddMany("rbac.permission_code_long", map[Locale]string{
		LocaleEN: "permission code must be at most 100 characters",
		LocaleID: "kode izin maksimal 100 karakter",
	})
	b.AddMany("rbac.permission_code_invalid", map[Locale]string{
		LocaleEN: "permission code may only contain letters, digits, '.', '_' and '-'",
		LocaleID: "kode izin hanya boleh berisi huruf, angka, '.', '_' dan '-'",
	})
	b.AddMany("rbac.role_not_found", map[Locale]string{
		LocaleEN: "role not found",
		LocaleID: "peran tidak ditemukan",
	})
	b.AddMany("rbac.role_code_taken", map[Locale]string{
		LocaleEN: "role code already exists in this tenant",
		LocaleID: "kode peran sudah digunakan dalam tenant ini",
	})
	b.AddMany("rbac.role_is_system", map[Locale]string{
		LocaleEN: "system roles cannot be modified",
		LocaleID: "peran sistem tidak dapat diubah",
	})
	b.AddMany("rbac.role_invalid", map[Locale]string{
		LocaleEN: "role name is required",
		LocaleID: "nama peran wajib diisi",
	})
	b.AddMany("rbac.role_code_required", map[Locale]string{
		LocaleEN: "role code is required",
		LocaleID: "kode peran wajib diisi",
	})
	b.AddMany("rbac.role_code_long", map[Locale]string{
		LocaleEN: "role code must be at most 100 characters",
		LocaleID: "kode peran maksimal 100 karakter",
	})
	b.AddMany("rbac.role_code_invalid", map[Locale]string{
		LocaleEN: "role code may only contain letters, digits, '_' and '-'",
		LocaleID: "kode peran hanya boleh berisi huruf, angka, '_' dan '-'",
	})
	b.AddMany("rbac.user_role_not_found", map[Locale]string{
		LocaleEN: "user role not found",
		LocaleID: "user role tidak ditemukan",
	})

	// Menu
	b.AddMany("menu.not_found", map[Locale]string{
		LocaleEN: "menu not found",
		LocaleID: "menu tidak ditemukan",
	})
	b.AddMany("menu.code_taken", map[Locale]string{
		LocaleEN: "menu code already exists in this tenant",
		LocaleID: "kode menu sudah digunakan dalam tenant ini",
	})
	b.AddMany("menu.cycle", map[Locale]string{
		LocaleEN: "moving this menu under that parent would create a cycle",
		LocaleID: "memindahkan menu ini ke induk tersebut akan membentuk siklus",
	})
	b.AddMany("menu.label_required", map[Locale]string{
		LocaleEN: "menu label is required",
		LocaleID: "label menu wajib diisi",
	})
	b.AddMany("menu.parent_tenant_mismatch", map[Locale]string{
		LocaleEN: "parent menu belongs to a different tenant",
		LocaleID: "menu induk berasal dari tenant yang berbeda",
	})
	b.AddMany("menu.code_required", map[Locale]string{
		LocaleEN: "menu code is required",
		LocaleID: "kode menu wajib diisi",
	})
	b.AddMany("menu.code_long", map[Locale]string{
		LocaleEN: "menu code must be at most 100 characters",
		LocaleID: "kode menu maksimal 100 karakter",
	})
	b.AddMany("menu.code_invalid", map[Locale]string{
		LocaleEN: "menu code may not contain whitespace or slashes",
		LocaleID: "kode menu tidak boleh mengandung spasi atau garis miring",
	})

	// API keys (codes match internal/domain/apikey + middleware/apikey).
	b.AddMany("apikey.not_found", map[Locale]string{
		LocaleEN: "API key not found",
		LocaleID: "API key tidak ditemukan",
	})
	b.AddMany("apikey.invalid", map[Locale]string{
		LocaleEN: "invalid API key",
		LocaleID: "API key tidak valid",
	})
	b.AddMany("apikey.malformed", map[Locale]string{
		LocaleEN: "malformed API key",
		LocaleID: "format API key tidak valid",
	})
	b.AddMany("apikey.inactive", map[Locale]string{
		LocaleEN: "API key has been revoked or expired",
		LocaleID: "API key telah dicabut atau kedaluwarsa",
	})
	b.AddMany("apikey.name_required", map[Locale]string{
		LocaleEN: "API key name is required",
		LocaleID: "nama API key wajib diisi",
	})
	b.AddMany("apikey.user_session_required", map[Locale]string{
		LocaleEN: "this endpoint requires an interactive user session, not an API key",
		LocaleID: "endpoint ini hanya dapat diakses dengan sesi pengguna, bukan API key",
	})

	// Sessions (self-service device list).
	b.AddMany("session.not_found", map[Locale]string{
		LocaleEN: "session not found",
		LocaleID: "sesi tidak ditemukan",
	})
	b.AddMany("session.disabled", map[Locale]string{
		LocaleEN: "session management is not enabled on this server",
		LocaleID: "manajemen sesi tidak aktif pada server ini",
	})
	b.AddMany("auth.required", map[Locale]string{
		LocaleEN: "authentication required",
		LocaleID: "autentikasi diperlukan",
	})

	return b
}
