package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/logging"
	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

var auctionLog = logging.For("auction")

// auctionFail 让事务回调用 error 把 HTTP 状态和响应体一并带出，事务随之回滚。
type auctionFail struct {
	status int
	body   gin.H
}

func (e *auctionFail) Error() string {
	msg, _ := e.body["error"].(string)
	return msg
}

func newAuctionFail(status int, msg string) *auctionFail {
	return &auctionFail{status: status, body: gin.H{"error": msg}}
}

func writeAuctionError(c *gin.Context, err error, fallback string) {
	var f *auctionFail
	if errors.As(err, &f) {
		c.JSON(f.status, f.body)
		return
	}
	auctionLog.Error(fallback, "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": fallback})
}

type auctionListingResponse struct {
	models.AuctionListing
	SellerName string `json:"seller_name"`
}

func parseAuctionID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "挂单ID无效"})
		return 0, false
	}
	return uint(id), true
}

func isAuctionStatus(s string) bool {
	switch models.AuctionStatus(s) {
	case models.AuctionStatusActive, models.AuctionStatusSold, models.AuctionStatusCancelled:
		return true
	}
	return false
}

// likeContains 把用户输入转成 LIKE 的「包含」模式，转义 % 和 _，配合 ESCAPE '\' 使用。
func likeContains(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}

func saveWarehouse(tx *gorm.DB, userID uint, items []models.Item) error {
	return tx.Model(&models.User{}).Where("id = ?", userID).
		Update("warehouse", models.JSONField[[]models.Item]{Data: items}).Error
}

func nonNilItems(items []models.Item) []models.Item {
	if items == nil {
		return []models.Item{}
	}
	return items
}

func respondAuctionPage(c *gin.Context, base *gorm.DB, order string, page, pageSize int) {
	base = base.Session(&gorm.Session{})

	var total int64
	if err := base.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询挂单总数失败"})
		return
	}

	rows := make([]models.AuctionListing, 0)
	if err := base.
		Preload("Seller", func(db *gorm.DB) *gorm.DB { return db.Select("id", "username") }).
		Order(order).
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询挂单列表失败"})
		return
	}

	items := make([]auctionListingResponse, len(rows))
	for i, l := range rows {
		items[i] = auctionListingResponse{AuctionListing: l, SellerName: l.Seller.Username}
	}
	c.JSON(http.StatusOK, newPaginatedResponse(items, page, pageSize, total))
}

// ListAuctionListings 返回在售挂单，支持按物品名搜索、按类型筛选和排序。
func ListAuctionListings(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}

	base := models.DB.Model(&models.AuctionListing{}).Where("status = ?", models.AuctionStatusActive)
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		base = base.Where(`item_name LIKE ? ESCAPE '\'`, likeContains(q))
	}
	if t := c.Query("type"); t != "" {
		if !models.IsInventoryItemType(models.ItemType(t)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "物品类型无效"})
			return
		}
		base = base.Where("item_type = ?", t)
	}

	var order string
	switch c.DefaultQuery("sort", "newest") {
	case "newest":
		order = "created_at DESC, id DESC"
	case "price_asc":
		order = "price ASC, id ASC"
	case "price_desc":
		order = "price DESC, id DESC"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "排序方式无效"})
		return
	}
	respondAuctionPage(c, base, order, page, pageSize)
}

// ListMyAuctionListings 返回当前用户自己的挂单，可按状态筛选。
func ListMyAuctionListings(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}

	base := models.DB.Model(&models.AuctionListing{}).Where("seller_id = ?", c.GetUint("user_id"))
	if s := c.Query("status"); s != "" {
		if !isAuctionStatus(s) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "挂单状态无效"})
			return
		}
		base = base.Where("status = ?", s)
	}
	respondAuctionPage(c, base, "created_at DESC, id DESC", page, pageSize)
}

// CreateAuctionListing 把仓库里的物品挂到拍卖行：物品立即从仓库移出，由挂单托管。
func CreateAuctionListing(c *gin.Context) {
	userID := c.GetUint("user_id")

	var req struct {
		Item  models.Item `json:"item"`
		Count int         `json:"count"`
		Price int         `json:"price"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Item.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}
	if req.Count < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量无效"})
		return
	}
	if req.Price < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "价格必须大于 0"})
		return
	}

	var listing models.AuctionListing
	var warehouse []models.Item
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		rest, taken, hit := models.TakeItem(user.Warehouse.Data, name, req.Item.Desc, req.Count)
		if !hit {
			return newAuctionFail(http.StatusBadRequest, "该物品不在仓库中")
		}
		// NOTE: TakeItem 在请求数量不小于库存时整堆取走，这里要求数量必须不超过库存，免得挂单数量与玩家填写的不一致。
		if taken.Count != req.Count {
			return newAuctionFail(http.StatusBadRequest, "数量超过仓库持有量")
		}
		if err := saveWarehouse(tx, userID, rest); err != nil {
			return err
		}
		listing = models.AuctionListing{
			SellerID: userID,
			ItemName: taken.Name,
			ItemDesc: taken.Desc,
			ItemType: taken.Type,
			Count:    taken.Count,
			Price:    req.Price,
			Status:   models.AuctionStatusActive,
		}
		if err := tx.Create(&listing).Error; err != nil {
			return err
		}
		warehouse = rest
		return nil
	})
	if err != nil {
		writeAuctionError(c, err, "上架失败")
		return
	}

	auctionLog.Info("listed", "listing_id", listing.ID, "seller_id", userID, "item", listing.Item().Label(), "price", listing.Price)
	c.JSON(http.StatusCreated, gin.H{
		"message":   "已上架",
		"listing":   listing,
		"warehouse": nonNilItems(warehouse),
	})
}

// BuyAuctionListing 用金币买下整张挂单：买家扣款、卖家入账、物品进买家仓库，三者在同一事务里完成。
func BuyAuctionListing(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, ok := parseAuctionID(c)
	if !ok {
		return
	}

	var listing models.AuctionListing
	var buyer models.User
	var warehouse []models.Item
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		// NOTE: 先用条件 UPDATE 抢占挂单，同一张挂单只会成交一次；它也是事务里的第一条写语句，
		// SQLite 因此先拿到写锁，后面对仓库的读改写不会读到过期数据。
		claim := tx.Model(&models.AuctionListing{}).
			Where("id = ? AND status = ? AND seller_id <> ?", id, models.AuctionStatusActive, userID).
			Updates(map[string]any{"status": models.AuctionStatusSold, "buyer_id": userID, "closed_at": time.Now()})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			var l models.AuctionListing
			if err := tx.First(&l, id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return newAuctionFail(http.StatusNotFound, "挂单不存在")
				}
				return err
			}
			if l.SellerID == userID && l.Status == models.AuctionStatusActive {
				return newAuctionFail(http.StatusBadRequest, "不能购买自己上架的物品")
			}
			return newAuctionFail(http.StatusConflict, "该挂单已成交或已下架")
		}
		if err := tx.First(&listing, id).Error; err != nil {
			return err
		}

		debit := tx.Model(&models.User{}).
			Where("id = ? AND coins >= ?", userID, listing.Price).
			Update("coins", gorm.Expr("coins - ?", listing.Price))
		if debit.Error != nil {
			return debit.Error
		}
		if debit.RowsAffected == 0 {
			var cur models.User
			if err := tx.Select("coins").First(&cur, userID).Error; err != nil {
				return err
			}
			return &auctionFail{status: http.StatusPaymentRequired, body: gin.H{"error": "金币不足", "need": listing.Price, "current": cur.Coins}}
		}
		if err := tx.Model(&models.User{}).Where("id = ?", listing.SellerID).
			Update("coins", gorm.Expr("coins + ?", listing.Price)).Error; err != nil {
			return err
		}

		if err := tx.First(&buyer, userID).Error; err != nil {
			return err
		}
		warehouse = models.AddItem(buyer.Warehouse.Data, listing.Item())
		return saveWarehouse(tx, userID, warehouse)
	})
	if err != nil {
		writeAuctionError(c, err, "购买失败")
		return
	}

	auctionLog.Info("sold", "listing_id", listing.ID, "seller_id", listing.SellerID, "buyer_id", userID, "price", listing.Price)
	c.JSON(http.StatusOK, gin.H{
		"message":   "购买成功",
		"coins":     buyer.Coins,
		"listing":   listing,
		"warehouse": warehouse,
	})
}

// CancelAuctionListing 下架自己的在售挂单，托管的物品退回卖家仓库。
func CancelAuctionListing(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, ok := parseAuctionID(c)
	if !ok {
		return
	}

	var listing models.AuctionListing
	var warehouse []models.Item
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		claim := tx.Model(&models.AuctionListing{}).
			Where("id = ? AND seller_id = ? AND status = ?", id, userID, models.AuctionStatusActive).
			Updates(map[string]any{"status": models.AuctionStatusCancelled, "closed_at": time.Now()})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			var l models.AuctionListing
			if err := tx.Where("id = ? AND seller_id = ?", id, userID).First(&l).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return newAuctionFail(http.StatusNotFound, "挂单不存在")
				}
				return err
			}
			return newAuctionFail(http.StatusConflict, "该挂单已成交或已下架")
		}
		if err := tx.First(&listing, id).Error; err != nil {
			return err
		}

		var seller models.User
		if err := tx.First(&seller, userID).Error; err != nil {
			return err
		}
		warehouse = models.AddItem(seller.Warehouse.Data, listing.Item())
		return saveWarehouse(tx, userID, warehouse)
	})
	if err != nil {
		writeAuctionError(c, err, "下架失败")
		return
	}

	auctionLog.Info("cancelled", "listing_id", listing.ID, "seller_id", userID)
	c.JSON(http.StatusOK, gin.H{
		"message":   "已下架，物品已退回仓库",
		"listing":   listing,
		"warehouse": warehouse,
	})
}
