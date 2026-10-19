package catalog

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the catalog endpoints of tenant hosts.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Tenant is the tenant-host group behind session, host guard, CSRF, entitlement and support
	// read-only.
	Tenant *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
}

// Register adds the catalog routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil {
		panic("catalog: Register needs the tenant group and the command router")
	}
	g := r.Tenant
	g.GET("/colors", h.listColors)
	g.GET("/units", h.listUnits)
	g.POST("/units", h.createUnit)
	g.PATCH("/units/:id", h.updateUnit)
	g.GET("/tax-rates", h.listTaxRates)
	g.POST("/tax-rates", h.createTaxRate)
	g.PATCH("/tax-rates/:id", h.updateTaxRate)

	g.GET("/attributes", h.listAttributes)
	g.POST("/attributes", h.createAttribute)
	g.GET("/attributes/:id", h.getAttribute)
	g.PATCH("/attributes/:id", h.updateAttribute)
	g.DELETE("/attributes/:id", h.deleteAttribute)
	g.POST("/attributes/:id/options", h.addOption)
	g.PATCH("/attributes/:id/options/:optionId", h.updateOption)
	g.DELETE("/attributes/:id/options/:optionId", h.deleteOption)
	g.GET("/attribute-sets", h.listAttributeSets)
	g.POST("/attribute-sets", h.createAttributeSet)
	g.GET("/attribute-sets/:id", h.getAttributeSet)
	g.PATCH("/attribute-sets/:id", h.updateAttributeSet)
	g.PUT("/attribute-sets/:id/items", h.replaceSetItems)

	g.GET("/categories", h.listCategories)
	g.POST("/categories", h.createCategory)
	g.PATCH("/categories/:id", h.updateCategory)
	g.DELETE("/categories/:id", h.deleteCategory)

	g.GET("/products", h.listProducts)
	g.POST("/products", h.createProduct)
	r.Commands(g, "products").Collection("quick-create", h.quickCreate)
	g.GET("/products/:id", h.getProduct)
	g.PATCH("/products/:id", h.updateProduct)
	g.DELETE("/products/:id", h.deleteProduct)
	g.GET("/products/:id/variant-axes", h.getAxes)
	g.PUT("/products/:id/variant-axes", h.replaceAxes)
	g.POST(`/products/:id/variants\:preview`, h.previewVariants)
	g.POST("/products/:id/variants", h.generateVariants)
	g.GET("/products/:id/images", h.listImages)
	g.POST("/products/:id/images", h.addImage)
	g.DELETE("/products/:id/images/:imageId", h.deleteImage)

	g.GET("/skus", h.listSkus)
	g.GET("/skus/:id", h.getSku)
	g.PATCH("/skus/:id", h.updateSku)
	g.DELETE("/skus/:id", h.deleteSku)
	g.GET("/skus/:id/units", h.listSkuUnits)
	g.POST("/skus/:id/units", h.addSkuUnit)
	g.PATCH("/skus/:id/units/:unitId", h.updateSkuUnit)
	g.DELETE("/skus/:id/units/:unitId", h.deleteSkuUnit)
	g.GET("/skus/:id/barcodes", h.listBarcodes)
	g.POST("/skus/:id/barcodes", h.addBarcode)
	g.DELETE("/skus/:id/barcodes/:barcodeId", h.deleteBarcode)

	g.GET("/mix-groups", h.listMixGroups)
	g.POST("/mix-groups", h.createMixGroup)
	g.GET("/mix-groups/:id", h.getMixGroup)
	g.PATCH("/mix-groups/:id", h.updateMixGroup)
	g.DELETE("/mix-groups/:id", h.deleteMixGroup)
	mix := r.Commands(g, "mix-groups")
	mix.Item("deactivate", h.setMixGroupStatus(StatusInactive))
	mix.Item("reactivate", h.setMixGroupStatus(StatusActive))

	g.GET("/search", h.search)
}

// Request helpers: each renders the error itself and reports whether the handler may go on.

// scopeOf is the tenant of the request; support sessions read in read-only transactions.
func scopeOf(c *gin.Context) (Scope, error) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		return Scope{}, apperr.NewUnauthenticated()
	}
	return Scope{TenantID: tenantID, ReadOnly: p.Kind == middleware.PrincipalSupport}, nil
}

func scopeFor(c *gin.Context) (Scope, bool) {
	s, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return Scope{}, false
	}
	return s, true
}

func actorFor(c *gin.Context) (audit.Actor, bool) {
	a, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return audit.Actor{}, false
	}
	return a, true
}

func bind[T any](c *gin.Context) (T, bool) {
	var v T
	if err := httpx.BindJSON(c, &v); err != nil {
		httpx.RenderError(c, err)
		return v, false
	}
	return v, true
}

func pathID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := httpx.PathID(c, name)
	if err != nil {
		httpx.RenderError(c, err)
		return uuid.Nil, false
	}
	return id, true
}

func ifMatch(c *gin.Context) (int, bool) {
	v, err := httpx.IfMatch(c)
	if err != nil {
		httpx.RenderError(c, err)
		return 0, false
	}
	return v, true
}

// respond renders err, or body with status; a positive version becomes the ETag.
func respond(c *gin.Context, status int, version int, body any, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	if version > 0 {
		httpx.SetETag(c, version)
	}
	c.JSON(status, body)
}

func noContent(c *gin.Context, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listOf is an unpaged list in the list envelope.
func listOf[T any](items []T) httpx.List[T] {
	if items == nil {
		items = []T{}
	}
	return httpx.List[T]{Data: items}
}

func queryUUID(c *gin.Context, name string) (*uuid.UUID, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	id, err := ids.Parse(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid"}))
		return nil, false
	}
	return &id, true
}

func queryBool(c *gin.Context, name string) (bool, bool) {
	raw := c.Query(name)
	if raw == "" {
		return false, true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid"}))
		return false, false
	}
	return v, true
}

func (h *Handler) listColors(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.Colors(c.Request.Context(), scope)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toColorDTO)), err)
}

func (h *Handler) listUnits(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	active, ok := queryBool(c, "active")
	if !ok {
		return
	}
	out, err := h.svc.Units(c.Request.Context(), scope, active)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toUnitDTO)), err)
}

func (h *Handler) createUnit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[unitRequest](c)
	if !ok {
		return
	}
	u, err := h.svc.CreateUnit(c.Request.Context(), actor, UnitInput(req))
	respond(c, http.StatusCreated, 0, toUnitDTO(u), err)
}

func (h *Handler) updateUnit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[unitRequest](c)
	if !ok {
		return
	}
	u, err := h.svc.UpdateUnit(c.Request.Context(), actor, id, UnitInput(req))
	respond(c, http.StatusOK, 0, toUnitDTO(u), err)
}

func (h *Handler) listTaxRates(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	active, ok := queryBool(c, "active")
	if !ok {
		return
	}
	out, err := h.svc.TaxRates(c.Request.Context(), scope, active)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toTaxRateDTO)), err)
}

func (h *Handler) createTaxRate(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[taxRateRequest](c)
	if !ok {
		return
	}
	t, err := h.svc.CreateTaxRate(c.Request.Context(), actor, TaxRateInput(req))
	respond(c, http.StatusCreated, 0, toTaxRateDTO(t), err)
}

func (h *Handler) updateTaxRate(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[taxRateRequest](c)
	if !ok {
		return
	}
	t, err := h.svc.UpdateTaxRate(c.Request.Context(), actor, id, TaxRateInput(req))
	respond(c, http.StatusOK, 0, toTaxRateDTO(t), err)
}

func (h *Handler) listAttributes(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.Attributes(c.Request.Context(), scope, AttributeFilter{Status: c.Query("status")})
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toAttributeDTO)), err)
}

func (h *Handler) getAttribute(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	a, err := h.svc.Attribute(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, a.Version, toAttributeDTO(a), err)
}

func (h *Handler) createAttribute(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[attributeRequest](c)
	if !ok {
		return
	}
	res, err := h.svc.CreateAttribute(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, res.Attribute.Version, toAttributeResultDTO(res), err)
}

func (h *Handler) updateAttribute(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	expected, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[attributeRequest](c)
	if !ok {
		return
	}
	res, err := h.svc.UpdateAttribute(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, res.Attribute.Version, toAttributeResultDTO(res), err)
}

func (h *Handler) deleteAttribute(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteAttribute(c.Request.Context(), actor, id))
}

func (h *Handler) addOption(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[optionRequest](c)
	if !ok {
		return
	}
	o, err := h.svc.AddOption(c.Request.Context(), actor, id, OptionInput(req))
	respond(c, http.StatusCreated, 0, toOptionDTO(o), err)
}

func (h *Handler) updateOption(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	optionID, ok := pathID(c, "optionId")
	if !ok {
		return
	}
	req, ok := bind[optionRequest](c)
	if !ok {
		return
	}
	o, err := h.svc.UpdateOption(c.Request.Context(), actor, id, optionID, OptionInput(req))
	respond(c, http.StatusOK, 0, toOptionDTO(o), err)
}

func (h *Handler) deleteOption(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	optionID, ok := pathID(c, "optionId")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteOption(c.Request.Context(), actor, id, optionID))
}

func (h *Handler) listAttributeSets(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.AttributeSets(c.Request.Context(), scope)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toAttributeSetDTO)), err)
}

func (h *Handler) getAttributeSet(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	s, err := h.svc.AttributeSet(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, 0, toAttributeSetDTO(s), err)
}

func (h *Handler) createAttributeSet(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[attributeSetRequest](c)
	if !ok {
		return
	}
	s, err := h.svc.CreateAttributeSet(c.Request.Context(), actor, AttributeSetInput{Name: req.Name, Active: req.Active, Items: setItems(req.Items)})
	respond(c, http.StatusCreated, 0, toAttributeSetDTO(s), err)
}

func (h *Handler) updateAttributeSet(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[attributeSetRequest](c)
	if !ok {
		return
	}
	s, err := h.svc.UpdateAttributeSet(c.Request.Context(), actor, id, AttributeSetInput{Name: req.Name, Active: req.Active, Items: setItems(req.Items)})
	respond(c, http.StatusOK, 0, toAttributeSetDTO(s), err)
}

func (h *Handler) replaceSetItems(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[setItemsRequest](c)
	if !ok {
		return
	}
	items := setItems(req.Items)
	if items == nil {
		items = []SetItem{}
	}
	res, err := h.svc.ReplaceAttributeSetItems(c.Request.Context(), actor, id, items)
	hidden := res.Hidden
	if hidden == nil {
		hidden = []string{}
	}
	respond(c, http.StatusOK, 0, setItemsResultDTO{attributeSetDTO: toAttributeSetDTO(res.Set), HiddenAttributes: hidden}, err)
}

func (h *Handler) listCategories(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.Categories(c.Request.Context(), scope)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toCategoryDTO)), err)
}

func (h *Handler) createCategory(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[categoryRequest](c)
	if !ok {
		return
	}
	cat, err := h.svc.CreateCategory(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, 0, toCategoryDTO(cat), err)
}

func (h *Handler) updateCategory(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[categoryRequest](c)
	if !ok {
		return
	}
	cat, err := h.svc.UpdateCategory(c.Request.Context(), actor, id, req.input())
	respond(c, http.StatusOK, 0, toCategoryDTO(cat), err)
}

func (h *Handler) deleteCategory(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteCategory(c.Request.Context(), actor, id))
}

func (h *Handler) listProducts(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	category, ok := queryUUID(c, "category_id")
	if !ok {
		return
	}
	f := ProductFilter{Q: c.Query("q"), CategoryID: category, Status: c.Query("status"), Type: c.Query("type"),
		Limit: page.FetchLimit()}
	if raw := c.Query("attributes"); raw != "" {
		f.Attributes = json.RawMessage(raw)
	}
	if page.After != nil {
		f.AfterKey, f.AfterID = &page.After.SortKey, page.After.ID
	}
	rows, err := h.svc.Products(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(p ProductListItem) httpx.Cursor {
		return httpx.Cursor{SortKey: p.NameKey, ID: p.ID}
	})
	c.JSON(http.StatusOK, httpx.List[productListItemDTO]{NextCursor: list.NextCursor,
		Data: mapSlice(list.Data, func(p ProductListItem) productListItemDTO {
			return productListItemDTO{productDTO: toProductDTO(p.Product), SkuCount: p.SkuCount}
		})})
}

func (h *Handler) getProduct(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Product(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, d.Product.Version, toProductDetailDTO(d), err)
}

func (h *Handler) createProduct(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[productCreateRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.CreateProduct(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, d.Product.Version, toProductDetailDTO(d), err)
}

func (h *Handler) updateProduct(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	expected, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[productPatchRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.UpdateProduct(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, d.Product.Version, toProductDetailDTO(d), err)
}

func (h *Handler) deleteProduct(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteProduct(c.Request.Context(), actor, id))
}

func (h *Handler) quickCreate(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[quickCreateRequest](c)
	if !ok {
		return
	}
	res, err := h.svc.QuickCreateProduct(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, 0, quickCreateDTO{Product: toProductDetailDTO(res.Product), SalePriceSaved: res.SalePriceSaved}, err)
}

func (h *Handler) getAxes(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	axes, err := h.svc.VariantAxes(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, 0, listOf(mapSlice(axes, toAxisDTO)), err)
}

func (h *Handler) replaceAxes(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[axesRequest](c)
	if !ok {
		return
	}
	axes, err := h.svc.ReplaceVariantAxes(c.Request.Context(), actor, id, req.AttributeIDs)
	respond(c, http.StatusOK, 0, listOf(mapSlice(axes, toAxisDTO)), err)
}

func (h *Handler) previewVariants(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[variantRequest](c)
	if !ok {
		return
	}
	rows, err := h.svc.PreviewVariants(c.Request.Context(), scope, id, req.input())
	respond(c, http.StatusOK, 0, listOf(mapSlice(rows, toVariantRowDTO)), err)
}

func (h *Handler) generateVariants(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[variantRequest](c)
	if !ok {
		return
	}
	res, err := h.svc.GenerateVariants(c.Request.Context(), actor, id, req.input())
	status := http.StatusCreated
	if len(res.Created) == 0 {
		status = http.StatusOK
	}
	respond(c, status, 0, variantResultDTO{Created: mapSlice(res.Created, toSkuDetailDTO), Existing: res.Existing}, err)
}

func (h *Handler) listImages(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Images(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toImageDTO)), err)
}

func (h *Handler) addImage(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[imageRequest](c)
	if !ok {
		return
	}
	img, err := h.svc.AddImage(c.Request.Context(), actor, id, ImageInput(req))
	respond(c, http.StatusCreated, 0, toImageDTO(img), err)
}

func (h *Handler) deleteImage(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	imageID, ok := pathID(c, "imageId")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteImage(c.Request.Context(), actor, id, imageID))
}

func (h *Handler) listSkus(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	product, ok := queryUUID(c, "product_id")
	if !ok {
		return
	}
	category, ok := queryUUID(c, "category_id")
	if !ok {
		return
	}
	var skuIDs []uuid.UUID
	for _, raw := range c.QueryArray("id") {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"id": "invalid"}))
			return
		}
		skuIDs = append(skuIDs, id)
	}
	f := SkuFilter{Q: c.Query("q"), ProductID: product, CategoryID: category, Type: c.Query("type"), Status: c.Query("status"),
		IDs: skuIDs, Limit: page.FetchLimit()}
	if page.After != nil {
		f.AfterCode, f.AfterID = &page.After.SortKey, page.After.ID
	}
	rows, err := h.svc.Skus(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(s SkuListItem) httpx.Cursor { return httpx.Cursor{SortKey: s.Code, ID: s.ID} })
	c.JSON(http.StatusOK, httpx.List[skuListItemDTO]{Data: mapSlice(list.Data, toSkuListItemDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getSku(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Sku(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, d.Sku.Version, toSkuDetailDTO(d), err)
}

func (h *Handler) updateSku(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	expected, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[skuPatchRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.UpdateSku(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, d.Sku.Version, toSkuDetailDTO(d), err)
}

func (h *Handler) deleteSku(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteSku(c.Request.Context(), actor, id))
}

func (h *Handler) listSkuUnits(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.SkuUnits(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toSkuUnitDTO)), err)
}

func (h *Handler) addSkuUnit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[skuUnitRequest](c)
	if !ok {
		return
	}
	u, err := h.svc.AddSkuUnit(c.Request.Context(), actor, id, req.input(req.UnitID))
	respond(c, http.StatusCreated, 0, toSkuUnitDTO(u), err)
}

func (h *Handler) updateSkuUnit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	unitID, ok := pathID(c, "unitId")
	if !ok {
		return
	}
	req, ok := bind[skuUnitPatchRequest](c)
	if !ok {
		return
	}
	u, err := h.svc.UpdateSkuUnit(c.Request.Context(), actor, id, unitID, req.input(unitID))
	respond(c, http.StatusOK, 0, toSkuUnitDTO(u), err)
}

func (h *Handler) deleteSkuUnit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	unitID, ok := pathID(c, "unitId")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteSkuUnit(c.Request.Context(), actor, id, unitID))
}

func (h *Handler) listBarcodes(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Barcodes(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toBarcodeDTO)), err)
}

func (h *Handler) addBarcode(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[barcodeRequest](c)
	if !ok {
		return
	}
	b, err := h.svc.AddBarcode(c.Request.Context(), actor, id, BarcodeInput(req))
	respond(c, http.StatusCreated, 0, toBarcodeDTO(b), err)
}

func (h *Handler) deleteBarcode(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	barcodeID, ok := pathID(c, "barcodeId")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteBarcode(c.Request.Context(), actor, id, barcodeID))
}

func (h *Handler) listMixGroups(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.MixGroups(c.Request.Context(), scope, c.Query("status"))
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toMixGroupDTO)), err)
}

func (h *Handler) getMixGroup(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	g, err := h.svc.MixGroup(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, g.Version, toMixGroupDTO(g), err)
}

func (h *Handler) createMixGroup(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[mixGroupRequest](c)
	if !ok {
		return
	}
	g, err := h.svc.CreateMixGroup(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, g.Version, toMixGroupDTO(g), err)
}

func (h *Handler) updateMixGroup(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	expected, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[mixGroupRequest](c)
	if !ok {
		return
	}
	g, err := h.svc.UpdateMixGroup(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, g.Version, toMixGroupDTO(g), err)
}

func (h *Handler) deleteMixGroup(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteMixGroup(c.Request.Context(), actor, id))
}

func (h *Handler) setMixGroupStatus(status string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := actorFor(c)
		if !ok {
			return
		}
		g, err := h.svc.SetMixGroupStatus(c.Request.Context(), actor, httpx.CommandID(c), status)
		respond(c, http.StatusOK, g.Version, toMixGroupDTO(g), err)
	}
}

func (h *Handler) search(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	inStock, ok := queryBool(c, "in_stock")
	if !ok {
		return
	}
	req := SearchRequest{Kind: c.Query("type"), Q: c.Query("q"), InStock: inStock}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"limit": "out_of_range"}))
			return
		}
		req.Limit = n
	}
	items, err := h.svc.Search(c.Request.Context(), scope, req)
	respond(c, http.StatusOK, 0, listOf(mapSlice(items, toSearchItemDTO)), err)
}
