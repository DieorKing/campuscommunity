package code

type ResCode int64

const (
	//通用模块
	CodeSuccess      ResCode = 0
	CodeInvalidParam ResCode = 1001
	CodeNotFound     ResCode = 1002
	CodeForbidden    ResCode = 1003
	CodeServerBusy   ResCode = 1004

	//用户模块
	CodeUserExist    ResCode = 10001 //用户名已存在
	CodeWeakPassword ResCode = 10002 //密码强度不足
	CodeNeedLogin    ResCode = 10003
	CodeInvalidToken ResCode = 10004
	CodeWrongLogin   ResCode = 10005 //用户名或密码错误

	//拼单模块
	CodeGroupBuyNotExist ResCode = 20001 //拼单不存在
	CodeGroupBuyExpired  ResCode = 20002 //拼单已截止
	CodeGroupBuyInvalid  ResCode = 20003 //拼单参数不合法
)

var codeMesMap = map[ResCode]string{
	CodeSuccess:      "success",
	CodeInvalidParam: "请求参数错误", //Gin ShouldBind 失败、参数格式不对
	CodeNotFound:     "资源不存在",
	CodeForbidden:    "无权限", //资源所有者校验不通过
	CodeServerBusy:   "服务器繁忙",
	CodeUserExist:    "用户名已存在",
	CodeWeakPassword: "密码强度不足（需≥8位且含字母和数字）",
	CodeNeedLogin:    "需要登录",
	CodeInvalidToken: "无效的Token",
	CodeWrongLogin:   "用户名或密码错误",

	CodeGroupBuyNotExist: "拼单不存在",
	CodeGroupBuyExpired:  "拼单已截止",
	CodeGroupBuyInvalid:  "拼单参数不合法",
}

func (c ResCode) Msg() string {
	msg, ok := codeMesMap[c]
	if !ok {
		msg = codeMesMap[CodeServerBusy]
	}
	return msg
}
