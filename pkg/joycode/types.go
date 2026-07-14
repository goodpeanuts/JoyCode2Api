package joycode

// ModelInfo describes a JoyCode AI model (full fields from upstream joycode_modelList).
type ModelInfo struct {
	Label              string                 `json:"label"`
	ChatAPIModel       string                 `json:"chatApiModel"`
	Description        string                 `json:"description"`
	SystemMessage      string                 `json:"systemMessage"`
	Avatar             string                 `json:"avatar"`
	MaxTotalTokens     int                    `json:"maxTotalTokens"`
	RespMaxTokens      int                    `json:"respMaxTokens"`
	Temperature        float64                `json:"temperature"`
	Features           []string               `json:"features"`
	SupportStream      bool                   `json:"supportStream"`
	IsHidden           bool                   `json:"isHidden"`
	Hidden             bool                   `json:"hidden"`
	IsPreferred        bool                   `json:"isPreferred"`
	Prefer             bool                   `json:"prefer"`
	SortOrder          int                    `json:"sortOrder"`
	ModelFunctionType  string                 `json:"modelFunctionType"`
	VerificationStatus string                 `json:"verificationStatus"`
	ModelID            string                 `json:"modelId"`
	CreateTime         int64                  `json:"createTime"`
	Ext                string                 `json:"ext"`
	ExtJson            map[string]interface{} `json:"extJson"`
}

// ModelStrategyItem represents one entry in model_strategy_config.
type ModelStrategyItem struct {
	ID        int    `json:"id"`
	TaskType  string `json:"taskType"`
	TaskName  string `json:"taskName"`
	AutoModel string `json:"autoModel"`
	Enable    bool   `json:"enable"`
	Display   bool   `json:"display"`
	Tenant    string `json:"tenant"`
	TenantID  int    `json:"tenantId"`
	Remark    string `json:"remark"`
}

// ErrorConfigItem represents one entry in model_error_config_get.
type ErrorConfigItem struct {
	ErrorShowType string `json:"errorShowType"`
	ErrorSource   string `json:"errorSource"`
	Priority      string `json:"priority"`
	StatusCode    string `json:"statusCode"`
	Desc          string `json:"desc"`
}

