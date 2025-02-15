package sqlite

import (
	"context"
	"time"

	"github.com/filecoin-project/go-address"

	"gorm.io/gorm"

	"github.com/filecoin-project/venus-messager/models/repo"
	"github.com/filecoin-project/venus-messager/types"
)

type sqliteAddress struct {
	ID     types.UUID `gorm:"column:id;type:varchar(256);primary_key"`
	Addr   string     `gorm:"column:addr;type:varchar(256);NOT NULL"` // 主键
	Nonce  uint64     `gorm:"column:nonce;type:unsigned bigint;index;NOT NULL"`
	Weight int64      `gorm:"column:weight;type:bigint;index;NOT NULL"`

	IsDeleted int       `gorm:"column:is_deleted;index;default:-1;NOT NULL"` // 是否删除 1:是  -1:否
	CreatedAt time.Time `gorm:"column:created_at;index;NOT NULL"`            // 创建时间
	UpdatedAt time.Time `gorm:"column:updated_at;index;NOT NULL"`            // 更新时间
}

func (s sqliteAddress) TableName() string {
	return "addresses"
}

func FromAddress(addr *types.Address) *sqliteAddress {
	return &sqliteAddress{
		ID:        addr.ID,
		Addr:      addr.Addr.String(),
		Nonce:     addr.Nonce,
		Weight:    addr.Weight,
		IsDeleted: addr.IsDeleted,
		CreatedAt: addr.CreatedAt,
		UpdatedAt: addr.UpdatedAt,
	}
}

func (s sqliteAddress) Address() (*types.Address, error) {
	addr, err := address.NewFromString(s.Addr)
	if err != nil {
		return nil, err
	}

	return &types.Address{
		ID:        s.ID,
		Addr:      addr,
		Nonce:     s.Nonce,
		Weight:    s.Weight,
		IsDeleted: s.IsDeleted,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}, nil
}

type sqliteAddressRepo struct {
	*gorm.DB
}

func newSqliteAddressRepo(db *gorm.DB) *sqliteAddressRepo {
	return &sqliteAddressRepo{DB: db}
}

func (s sqliteAddressRepo) SaveAddress(ctx context.Context, addr *types.Address) error {
	return s.DB.Save(FromAddress(addr)).Error
}

func (s sqliteAddressRepo) UpdateAddress(ctx context.Context, addr *types.Address) error {
	updateColumns := map[string]interface{}{
		"nonce":      addr.Nonce,
		"is_deleted": addr.IsDeleted,
	}
	return s.DB.Model((*sqliteAddress)(nil)).Where("addr = ?", addr.Addr.String()).Updates(updateColumns).Error
}

func (s sqliteAddressRepo) UpdateNonce(ctx context.Context, addr address.Address, nonce uint64) error {
	return s.DB.Model((*sqliteAddress)(nil)).Where("addr = ?", addr.String()).UpdateColumn("nonce", nonce).Error
}

func (s sqliteAddressRepo) HasAddress(ctx context.Context, addr address.Address) (bool, error) {
	var count int64
	err := s.DB.Model((*sqliteAddress)(nil)).Where("addr = ?", addr.String()).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s sqliteAddressRepo) GetAddress(ctx context.Context, addr address.Address) (*types.Address, error) {
	var a sqliteAddress
	if err := s.DB.Where("addr = ? and is_deleted = -1", addr.String()).First(&a).Error; err != nil {
		return nil, err
	}

	return a.Address()
}

func (s sqliteAddressRepo) GetOneRecord(ctx context.Context, addr address.Address) (*types.Address, error) {
	var a sqliteAddress
	if err := s.DB.Where("addr = ?", addr.String()).First(&a).Error; err != nil {
		return nil, err
	}

	return a.Address()
}

func (s sqliteAddressRepo) DelAddress(ctx context.Context, addr address.Address) error {
	return s.DB.Model((*sqliteAddress)(nil)).Where("addr = ? and is_deleted = -1", addr.String()).
		Updates(map[string]interface{}{"is_deleted": repo.Deleted}).Error
}

func (s sqliteAddressRepo) ListAddress(ctx context.Context) ([]*types.Address, error) {
	var list []*sqliteAddress
	if err := s.DB.Find(&list, "is_deleted = ?", -1).Error; err != nil {
		return nil, err
	}

	result := make([]*types.Address, len(list))
	for index, r := range list {
		addr, err := r.Address()
		if err != nil {
			return nil, err
		}
		result[index] = addr
	}

	return result, nil
}

var _ repo.AddressRepo = &sqliteAddressRepo{}
