// NOTE: companion.go 定义 AI 陪玩占用的内置 bot 用户。每个房间最多一个 AI，所以全站只需要一个 bot 用户；
// AI 席位的 SessionPlayer.UserID 就是它，点名、锁卡、可见性过滤都沿用按 UserID 的现有逻辑。
package models

import (
	"errors"
	"fmt"
)

const (
	companionUsername = "AI陪玩"
	companionEmail    = "companion@bot.invalid"
	// NOTE: 不是合法的 bcrypt 串，比对永远失败；登录处理器另外按角色直接拒绝。
	companionPasswordHash = "!"
)

// EnsureCompanionUser 返回 bot 用户的 ID，不存在就创建；可重复执行。
func EnsureCompanionUser() (uint, error) {
	if id := CompanionUserID(); id != 0 {
		return id, nil
	}
	name := companionUsername
	for i := 0; i < 20; i++ {
		var taken int64
		if err := DB.Model(&User{}).Where("username = ?", name).Count(&taken).Error; err != nil {
			return 0, err
		}
		if taken == 0 {
			break
		}
		name = fmt.Sprintf("%s%d", companionUsername, i+2)
	}
	u := User{
		Username:     name,
		Email:        companionEmail,
		PasswordHash: companionPasswordHash,
		Role:         RoleBot,
	}
	if err := DB.Create(&u).Error; err != nil {
		return 0, err
	}
	if u.ID == 0 {
		return 0, errors.New("create companion user: empty id")
	}
	return u.ID, nil
}

// CompanionUserID 每次都查库（users 表很小）：测试会替换 DB，不能缓存成包级变量。0 表示不存在。
func CompanionUserID() uint {
	if DB == nil {
		return 0
	}
	var u User
	if err := DB.Select("id").Where("role = ?", RoleBot).Order("id ASC").First(&u).Error; err != nil {
		return 0
	}
	return u.ID
}

func IsCompanionUser(id uint) bool {
	if id == 0 {
		return false
	}
	return CompanionUserID() == id
}
