package i18n

// DefaultBundle returns a Bundle pre-loaded with translations for
// every error code emitted by the framework's built-in modules
// (auth, validator, RBAC, menu, rate-limiter, request decoding).
// Apps may extend it via Add / AddMany before pinning with
// SetGlobal.
//
// Translations are intentionally short and direct — long sentences
// are awkward to render in toast notifications.
func DefaultBundle() *Bundle {
	b := NewBundle(LocaleEN)
	b.AddMany("internal", map[Locale]string{
		LocaleEN: "internal server error",
		LocaleID: "kesalahan internal pada server",
	})
	b.AddMany("validation", map[Locale]string{
		LocaleEN: "request validation failed",
		LocaleID: "data permintaan tidak valid",
	})
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
	b.AddMany("auth.missing_token", map[Locale]string{
		LocaleEN: "authentication required",
		LocaleID: "autentikasi diperlukan",
	})
	b.AddMany("auth.missing_user", map[Locale]string{
		LocaleEN: "authentication required",
		LocaleID: "autentikasi diperlukan",
	})
	b.AddMany("auth.invalid", map[Locale]string{
		LocaleEN: "invalid or expired token",
		LocaleID: "token tidak valid atau telah kedaluwarsa",
	})
	b.AddMany("auth.forbidden", map[Locale]string{
		LocaleEN: "you do not have permission to perform this action",
		LocaleID: "Anda tidak memiliki izin untuk melakukan tindakan ini",
	})
	b.AddMany("user.invalid_credentials", map[Locale]string{
		LocaleEN: "invalid email or password",
		LocaleID: "email atau kata sandi salah",
	})
	b.AddMany("user.email_taken", map[Locale]string{
		LocaleEN: "email already registered",
		LocaleID: "email sudah terdaftar",
	})
	b.AddMany("rate_limited", map[Locale]string{
		LocaleEN: "too many requests, slow down",
		LocaleID: "terlalu banyak permintaan, mohon perlambat",
	})
	b.AddMany("route.not_found", map[Locale]string{
		LocaleEN: "route not found",
		LocaleID: "rute tidak ditemukan",
	})
	b.AddMany("route.method_not_allowed", map[Locale]string{
		LocaleEN: "method not allowed",
		LocaleID: "metode HTTP tidak diizinkan",
	})
	b.AddMany("permission.not_found", map[Locale]string{
		LocaleEN: "permission not found",
		LocaleID: "izin tidak ditemukan",
	})
	b.AddMany("permission.taken", map[Locale]string{
		LocaleEN: "permission code already exists",
		LocaleID: "kode izin sudah digunakan",
	})
	b.AddMany("role.not_found", map[Locale]string{
		LocaleEN: "role not found",
		LocaleID: "peran tidak ditemukan",
	})
	b.AddMany("role.taken", map[Locale]string{
		LocaleEN: "role code already exists",
		LocaleID: "kode peran sudah digunakan",
	})
	b.AddMany("role.system", map[Locale]string{
		LocaleEN: "cannot modify a system role",
		LocaleID: "peran sistem tidak dapat diubah",
	})
	b.AddMany("menu.not_found", map[Locale]string{
		LocaleEN: "menu not found",
		LocaleID: "menu tidak ditemukan",
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
	return b
}
