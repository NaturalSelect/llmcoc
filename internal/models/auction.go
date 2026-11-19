package models

import "time"

type AuctionStatus string

const (
	AuctionStatusActive    AuctionStatus = "active"
	AuctionStatusSold      AuctionStatus = "sold"
	AuctionStatusCancelled AuctionStatus = "cancelled"
)

// NOTE: AuctionListing 是拍卖行里的一口价挂单。上架时物品从卖家仓库移出、记在挂单上（托管），
// 成交时进买家仓库，下架时退回卖家仓库；Count 件物品整单出售，价格是整单总价。
type AuctionListing struct {
	ID        uint          `gorm:"primaryKey;autoIncrement" json:"id"`
	SellerID  uint          `gorm:"not null;index" json:"seller_id"`
	BuyerID   *uint         `gorm:"index" json:"buyer_id"`
	ItemName  string        `gorm:"not null;size:200;index" json:"item_name"`
	ItemDesc  string        `gorm:"type:text" json:"item_desc"`
	ItemType  ItemType      `gorm:"not null;size:32" json:"item_type"`
	Count     int           `gorm:"not null" json:"count"`
	Price     int           `gorm:"not null" json:"price"`
	Status    AuctionStatus `gorm:"not null;size:16;index" json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	ClosedAt  *time.Time    `json:"closed_at"`
	Seller    User          `gorm:"foreignKey:SellerID" json:"-"`
}

// Item 还原挂单托管的物品，用于成交或下架时放回仓库。
func (l AuctionListing) Item() Item {
	return Item{Name: l.ItemName, Desc: l.ItemDesc, Count: l.Count, Type: l.ItemType}
}
