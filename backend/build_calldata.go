package backend

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	wcommon "github.com/incognitochain/incognito-web-based-backend/common"
	puniswap "github.com/incognitochain/incognito-web-based-backend/papps/puniswapproxy"
)

type UniswapQuote struct {
	Data struct {
		AmountIn         string           `json:"amountIn"`
		AmountOut        string           `json:"amountOut"`
		AmountOutRaw     string           `json:"amountOutRaw"`
		Route            [][]UniswapRoute `json:"route"`
		Impact           float64          `json:"impact"`
		EstimatedGasUsed string           `json:"estimatedGasUsed"`
	} `json:"data"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

type UniswapRoute struct {
	AmountIn          string            `json:"amountIn"`
	AmountOut         string            `json:"amountOut"`
	Fee               int64             `json:"fee"`
	Liquidity         string            `json:"liquidity"`
	Percent           float64           `json:"percent"`
	Type              string            `json:"type"`
	PoolAddress       string            `json:"poolAddress"`
	RawQuote          string            `json:"rawQuote"`
	SqrtPriceX96After string            `json:"sqrtPriceX96After"`
	TokenIn           UniswapQuoteToken `json:"tokenIn"`
	TokenOut          UniswapQuoteToken `json:"tokenOut"`
}

type UniswapQuoteToken struct {
	Address  string `json:"address"`
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	IsNative bool   `json:"isNative"`
}

var (
	NETWORK_ETH_ID   = 1
	WrappedNativeMap = map[int][]string{
		NETWORK_ETH_ID: {strings.ToLower("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"), strings.ToLower("0xb4fbf271143f4fbf7b91a5ded31805e42b2208d6")},
	}
)

func uniswapDataExtractor(data []byte) (*UniswapQuote, [][]int64, error) {
	if len(data) == 0 {
		return nil, nil, errors.New("can't extract data from empty byte array")
	}
	feePaths := [][]int64{}
	result := UniswapQuote{}
	err := json.Unmarshal(data, &result)
	if err != nil {
		return nil, nil, err
	}
	if result.Message != "ok" {
		return nil, nil, errors.New(result.Error)
	}
	for _, route := range result.Data.Route {
		fees := []int64{}
		for _, path := range route {
			fees = append(fees, path.Fee)
		}
		feePaths = append(feePaths, fees)
	}
	return &result, feePaths, nil
}

//      data := “acquire this data via https://docs.uniswap.org/sdk/v3/guides/quoting”
//      quote, feePaths, err := uniswapDataExtractor(data)
//      if err != nil {
//          return nil, err
//      }
//       paths := []ethcommon.Address{}
//       traversedTk := make(map[string]struct{})

//       for _, route := range quote.Data.Route[0] {
//                tokenAddress := ethcommon.Address{}
//                err = tokenAddress.UnmarshalText([]byte(route.TokenIn.Address))
//                if err != nil {
//                    return nil, err
//                }
//                if _, ok := traversedTk[route.TokenIn.Address]; !ok {
//                    paths = append(paths, tokenAddress)
//                }
//                traversedTk[route.TokenIn.Address] = struct{}{}

//                tokenAddress2 := ethcommon.Address{}
//                err = tokenAddress2.UnmarshalText([]byte(route.TokenOut.Address))
//                if err != nil {
//                    return nil, err
//                }
//                if _, ok := traversedTk[route.TokenOut.Address]; !ok {
//                    paths = append(paths, tokenAddress2)
//                }
//                traversedTk[route.TokenOut.Address] = struct{}{}
//            }

func CheckIsWrappedNativeToken(contractAddress string, network int) bool {
	list, exist := WrappedNativeMap[network]
	if exist {
		for _, v := range list {
			if strings.EqualFold(contractAddress, v) {
				return true
			}
		}
	}
	return false
}

func buildPathUniswap(paths []common.Address, fees []int64) []byte {
	var temp []byte
	for i := 0; i < len(fees); i++ {
		temp = append(temp, paths[i].Bytes()...)
		fee, err := hex.DecodeString(fmt.Sprintf("%06x", fees[i]))
		if err != nil {
			return nil
		}
		temp = append(temp, fee...)
	}
	temp = append(temp, paths[len(paths)-1].Bytes()...)

	return temp
}

func BuildCallDataUniswap(data []byte, tokenOutAddress string, srcQty *big.Int, expectedOut *big.Int) (string, error) {

	data = []byte("acquire this data via https://docs.uniswap.org/sdk/v3/guides/quoting")
	quote, feePaths, err := uniswapDataExtractor(data)
	if err != nil {
		return "", err
	}
	paths := []common.Address{}
	traversedTk := make(map[string]struct{})

	for _, route := range quote.Data.Route[0] {
		tokenAddress := common.Address{}
		err = tokenAddress.UnmarshalText([]byte(route.TokenIn.Address))
		if err != nil {
			return "", err
		}
		if _, ok := traversedTk[route.TokenIn.Address]; !ok {
			paths = append(paths, tokenAddress)
		}
		traversedTk[route.TokenIn.Address] = struct{}{}

		tokenAddress2 := common.Address{}
		err = tokenAddress2.UnmarshalText([]byte(route.TokenOut.Address))
		if err != nil {
			return "", err
		}
		if _, ok := traversedTk[route.TokenOut.Address]; !ok {
			paths = append(paths, tokenAddress2)
		}
		traversedTk[route.TokenOut.Address] = struct{}{}
	}

	uniswapProxy := common.HexToAddress("uniswapProxyContractAddress")
	recipient := common.HexToAddress("incognitoVaultContractAddress")
	isNativeOut := false
	if wcommon.CheckIsWrappedNativeToken(tokenOutAddress, 1) {
		isNativeOut = true
		recipient = uniswapProxy
	}
	var result string
	var input []byte

	tradeAbi, err := abi.JSON(strings.NewReader(puniswap.PuniswapMetaData.ABI))
	if err != nil {
		return result, err
	}

	if len(feePaths[0]) > 1 {
		agr := &puniswap.ISwapRouter2ExactInputParams{
			Path:             buildPathUniswap(paths, feePaths[0]),
			Recipient:        recipient,
			AmountIn:         srcQty,
			AmountOutMinimum: expectedOut,
		}

		input, err = tradeAbi.Pack("tradeInput", agr, isNativeOut)
	} else {
		agr := &puniswap.ISwapRouter2ExactInputSingleParams{
			TokenIn:           paths[0],
			TokenOut:          paths[len(paths)-1],
			Fee:               big.NewInt(feePaths[0][0]),
			Recipient:         recipient,
			AmountIn:          srcQty,
			SqrtPriceLimitX96: big.NewInt(0),
			AmountOutMinimum:  expectedOut,
		}

		input, err = tradeAbi.Pack("tradeInputSingle", agr, isNativeOut)
	}
	result = hex.EncodeToString(input)
	return result, err
}
