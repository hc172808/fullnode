package rpc

import "testing"

func TestEthSendRawTransactionFailsClosed(t *testing.T) {
	server := &Server{}

	tests := []struct {
		name   string
		params []interface{}
		code   int
	}{
		{
			name: "nonempty raw transaction",
			params: []interface{}{
				"0xdeadbeef",
			},
			code: -32000,
		},
		{
			name: "missing raw transaction",
			code: -32602,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := server.dispatch(jsonRPCRequest{
				Method: "eth_sendRawTransaction",
				Params: test.params,
				ID:     1,
			})

			if response.Result != nil {
				t.Fatalf("expected no result, got %#v", response.Result)
			}
			rpcError, ok := response.Error.(map[string]interface{})
			if !ok {
				t.Fatalf("expected JSON-RPC error object, got %#v", response.Error)
			}
			if got, ok := rpcError["code"].(int); !ok || got != test.code {
				t.Fatalf("error code = %#v, want %d", rpcError["code"], test.code)
			}
		})
	}
}
