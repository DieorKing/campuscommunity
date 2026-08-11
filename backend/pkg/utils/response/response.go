package response

import (
	code "campuscommunity/pkg/utils/code"
	"net/http"

	"github.com/gin-gonic/gin"
)

/*
`{code,msg,data,}`
*/
type ResponseData struct {
	Code code.ResCode `json:"code"`
	Msg  string       `json:"msg"`
	Data any          `json:"data"`
}

func ResponseError(c *gin.Context, code code.ResCode) {
	c.JSON(http.StatusOK, &ResponseData{
		Code: code,
		Msg:  code.Msg(),
		Data: nil,
	})
}

func ResponseErrorWithMsg(c *gin.Context, code code.ResCode, msg string) {
	c.JSON(http.StatusOK, &ResponseData{
		Code: code,
		Msg:  msg,
		Data: nil,
	})
}

func ResponseSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, &ResponseData{
		Code: code.CodeSuccess,
		Msg:  code.CodeSuccess.Msg(),
		Data: data,
	})
}
