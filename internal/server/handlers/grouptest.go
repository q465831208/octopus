package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/group").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/test-item", http.MethodPost).Handle(testGroupItem),
		).
		AddRoute(
			router.NewRoute("/test-all", http.MethodPost).Handle(testGroupAll),
		)
}

// testGroupItem 测试分组内指定 (channel_id, model_name) 能否正常对话。
func testGroupItem(c *gin.Context) {
	var req struct {
		ChannelID int    `json:"channel_id"`
		ModelName string `json:"model_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.ChannelID <= 0 {
		resp.Error(c, http.StatusBadRequest, "channel_id required")
		return
	}
	if strings.TrimSpace(req.ModelName) == "" {
		resp.Error(c, http.StatusBadRequest, "model_name required")
		return
	}
	channel, err := op.ChannelGet(req.ChannelID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "channel not found")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	resp.Success(c, op.TestGroupItemChannel(ctx, channel, strings.TrimSpace(req.ModelName)))
}

// testGroupAll 测试分组内所有项；失败的项自动移出分组，仅在可用项上做负载均衡。
func testGroupAll(c *gin.Context) {
	var req struct {
		GroupID int `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.GroupID <= 0 {
		resp.Error(c, http.StatusBadRequest, "group_id required")
		return
	}
	ctx := c.Request.Context()
	results, kept, removed, err := op.TestGroupItemsAndPrune(ctx, req.GroupID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, map[string]any{
		"tested":  len(results),
		"kept":    kept,
		"removed": removed,
		"results": results,
	})
}