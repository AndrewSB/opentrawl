package photos

// AssetReadiness is a plain description of one asset, not a macOS operation, so
// it lives outside the Darwin-only files. Platform-neutral archive code names
// this type; leaving it beside the PhotoKit bridge made every package that
// mentions an asset's readiness Darwin-only by accident.
// AssetReadiness is the signed-helper's small proof that one current,
// unlocated PhotoKit image has the identity and resource facts needed before
// any immutable-original or current-still export is attempted.
type AssetReadiness struct {
	LocalIdentifier  string `json:"local_identifier"`
	AssetUUID        string `json:"asset_uuid"`
	MediaType        string `json:"media_type"`
	HasLocation      bool   `json:"has_location"`
	CreationDate     string `json:"creation_date"`
	ModificationDate string `json:"modification_date"`
	PixelWidth       int64  `json:"pixel_width"`
	PixelHeight      int64  `json:"pixel_height"`
	OriginalFilename string `json:"original_filename"`
	OriginalUTI      string `json:"original_uti"`
}
