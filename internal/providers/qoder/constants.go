package qoder

const (
	OpenAPIBase   = "https://openapi.qoder.sh"
	CenterBase    = "https://center.qoder.sh"
	ChatBase      = "https://api3.qoder.sh"
	LoginURL      = "https://qoder.com/device/selectAccounts"
	DevicePollURL = OpenAPIBase + "/api/v1/deviceToken/poll"
	UserInfoURL   = OpenAPIBase + "/api/v1/userinfo"

	ChatSigPath    = "/api/v2/service/pro/sse/agent_chat_generation"
	ChatURL        = ChatBase + "/algo" + ChatSigPath + "?FetchKeys=llm_model_result&AgentId=agent_common"
	ChatURLEncoded = ChatURL + "&Encode=1"
	ModelListURL   = ChatBase + "/algo/api/v2/model/list"
	IDEVersion     = "1.0.0"
	ClientType     = "5"
	DataPolicy     = "disagree"
	LoginVersion   = "v2"
	MachineOS      = "x86_64_windows"
	MachineType    = "5"
	RSAPublicKey   = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`
)
