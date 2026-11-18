package taskauthz

// Catalog is the slice of the global catalog task authorization needs: the
// logical principal names that per-task configuration references, resolved to a
// token role or an OAuth2 client id.
//
// The composition root loads the catalog (internal/catalog) and injects this
// narrowed value, so nothing here performs file I/O or knows any on-disk format.
type Catalog struct {
	Roles   map[string]string // logical name -> token role
	Clients map[string]string // logical name -> client id
}
