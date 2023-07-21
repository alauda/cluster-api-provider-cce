package strings

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/rand"
)

func GenerateName(baseName string) string {
	// 使用随机字符串生成器生成一个唯一的随机字符串
	randomString := rand.String(5) // 生成长度为 5 的随机字符串

	// 将随机字符串与基本名称组合在一起
	name := fmt.Sprintf("%s%s", baseName, randomString)

	return name
}
