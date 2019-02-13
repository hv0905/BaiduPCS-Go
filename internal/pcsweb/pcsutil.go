package pcsweb

import (
	"fmt"
	"github.com/iikira/BaiduPCS-Go/baidupcs"
	"github.com/iikira/BaiduPCS-Go/internal/pcscommand"
	"github.com/iikira/BaiduPCS-Go/internal/pcsconfig"
	"github.com/iikira/BaiduPCS-Go/pcsutil/converter"
	"github.com/iikira/BaiduPCS-Go/pcsverbose"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

var (
	pcsCommandVerbose = pcsverbose.New("PCSCOMMAND")
	Version           = "3.6.4"
)

func PasswordHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	method := r.Form.Get("method")
	switch method {
	case "exist":
		password := pcsconfig.Config.AccessPass()
		if password != "" {
			sendHttpResponse(w, "", true)
			return
		}
		sendHttpResponse(w, "", false)
	case "verify":
		password := pcsconfig.Config.AccessPass()
		pass := r.Form.Get("password")
		if pass == password {
			sendHttpResponse(w, "", true)
			return
		}
		sendHttpResponse(w, "", false)
	case "set":
		password := pcsconfig.Config.AccessPass()
		oldpass := r.Form.Get("oldpass")
		if password != "" && oldpass != password {
			sendHttpErrorResponse(w, -3, "密码输入错误")
			return
		}

		pass := r.Form.Get("password")
		pcsconfig.Config.SetAccessPass(pass)
		if err := pcsconfig.Config.Save(); err != nil {
			sendHttpErrorResponse(w, -2, "保存配置错误: "+err.Error())
			return
		}
		sendHttpResponse(w, "", "")
	}
}

func LoginHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	bduss := r.Form.Get("bduss")
	b, err := pcsconfig.Config.SetupUserByBDUSS(bduss, "", "")
	if err != nil {
		sendHttpErrorResponse(w, -2, "BDUSS登录失败: "+err.Error())
		return
	}

	pcsconfig.Config.SwitchUser(&pcsconfig.BaiduBase{
		Name: b.Name,
	})

	if err = pcsconfig.Config.Save(); err != nil {
		sendHttpErrorResponse(w, -2, "保存配置错误: "+err.Error())
		return
	}

	sendHttpResponse(w, "账户登录成功", b)
}

func UserHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	method := r.Form.Get("method")
	switch method {
	case "list":
		sendHttpResponse(w, "", pcsconfig.Config.BaiduUserList())
	case "get":
		activeUser := pcsconfig.Config.ActiveUser()
		sendHttpResponse(w, "", activeUser)
	case "set":
		name := r.Form.Get("name")
		_, err := pcsconfig.Config.SwitchUser(&pcsconfig.BaiduBase{
			Name: name,
		})
		if err != nil {
			var uid uint64
			for _, user := range pcsconfig.Config.BaiduUserList() {
				if user.Name == name {
					uid = user.UID
				}
			}
			_, err = pcsconfig.Config.SwitchUser(&pcsconfig.BaiduBase{
				UID: uid,
			})
			if err != nil {
				sendHttpErrorResponse(w, -1, "切换用户失败: "+err.Error())
				return
			}
		}

		if err = pcsconfig.Config.Save(); err != nil {
			sendHttpErrorResponse(w, -2, "保存配置错误: "+err.Error())
			return
		}

		activeUser := pcsconfig.Config.ActiveUser()
		sendHttpResponse(w, "", activeUser)
	}
}

func QuotaHandle(w http.ResponseWriter, r *http.Request) {
	quota, used, _ := pcsconfig.Config.ActiveUserBaiduPCS().QuotaInfo()
	quotaMsg := fmt.Sprintf("{\"quota\": \"%s\", \"used\": \"%s\", \"un_used\": \"%s\", \"percent\": %.2f}",
		converter.ConvertFileSize(quota, 2),
		converter.ConvertFileSize(used, 2),
		converter.ConvertFileSize(quota-used, 2),
		100*float64(used)/float64(quota))
	pcsCommandVerbose.Info(quotaMsg)
	sendHttpResponse(w, "", quotaMsg)
}

func DownloadHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	method := r.Form.Get("method")
	id, _ := strconv.Atoi(r.Form.Get("id"))
	pcsCommandVerbose.Info("下载管理:" + method + ", " + r.Form.Get("id"))

	dl := DownloaderMap[id]
	if dl == nil {
		sendHttpErrorResponse(w, -6, "任务已经终结")
		return
	}

	response := &Response{
		Code: 0,
		Msg:  "success",
	}
	switch method {
	case "pause":
		dl.Pause()
	case "resume":
		dl.Resume()
	case "cancel":
		dl.Cancel()
	case "status":
		response.Data = dl.GetAllWorkersStatus()
	}
	w.Write(response.JSON())
}

func OfflineDownloadHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	method := r.Form.Get("method")
	pcsCommandVerbose.Info("离线下载:" + method)

	switch method {
	case "list":
		cl, err := pcsconfig.Config.ActiveUserBaiduPCS().CloudDlListTask()
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", cl)
	case "delete":
		id, _ := strconv.Atoi(r.Form.Get("id"))
		err := pcsconfig.Config.ActiveUserBaiduPCS().CloudDlDeleteTask(int64(id))
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", "")
	case "cancel":
		id, _ := strconv.Atoi(r.Form.Get("id"))
		err := pcsconfig.Config.ActiveUserBaiduPCS().CloudDlCancelTask(int64(id))
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", "")
	case "add":
		link := r.Form.Get("link")
		tpath := r.Form.Get("tpath")
		taskid, err := pcsconfig.Config.ActiveUserBaiduPCS().CloudDlAddTask(link, tpath)
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, strconv.Itoa(int(taskid)), "")
	}
}

func SearchHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	tpath := r.Form.Get("tpath")
	keyword := r.Form.Get("keyword")
	pcsCommandVerbose.Info("搜索:" + tpath + " " + keyword)

	files, err := pcsconfig.Config.ActiveUserBaiduPCS().Search(tpath, keyword, true)
	if err != nil {
		sendHttpErrorResponse(w, -1, err.Error())
		return
	}
	sendHttpResponse(w, "", files)
}

func RecycleHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	rmethod := r.Form.Get("method")
	pcsCommandVerbose.Info(rmethod)

	if rmethod == "list" {
		recycle, err := pcsconfig.Config.ActiveUserBaiduPCS().RecycleList(1)
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", recycle)
	}
	if rmethod == "clear" {
		_, err := pcsconfig.Config.ActiveUserBaiduPCS().RecycleClear()
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", "")
	}
	if rmethod == "restore" {
		rfid := r.Form.Get("fid")
		fid := converter.MustInt64(rfid)
		_, err := pcsconfig.Config.ActiveUserBaiduPCS().RecycleRestore(fid)
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", "")
	}
	if rmethod == "delete" {
		rfid := r.Form.Get("fid")
		fid := converter.MustInt64(rfid)
		err := pcsconfig.Config.ActiveUserBaiduPCS().RecycleDelete(fid)
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", "")
	}
}

func ShareHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	rmethod := r.Form.Get("method")
	pcsCommandVerbose.Info(rmethod)

	if rmethod == "list" {
		records, err := pcsconfig.Config.ActiveUserBaiduPCS().ShareList(1)
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", records)
	}
	if rmethod == "cancel" {
		rids := strings.Split(r.Form.Get("id"), ",")
		ids := make([]int64, 0, 10)
		for _, sid := range rids {
			tmp, _ := strconv.Atoi(sid)
			ids = append(ids, int64(tmp))
		}
		err := pcsconfig.Config.ActiveUserBaiduPCS().ShareCancel(ids)
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "success", "")
	}
	if rmethod == "set" {
		rpath := r.Form.Get("paths")
		rpaths := strings.Split(rpath, "|")
		paths := make([]string, 0, 10)
		for _, path := range rpaths {
			paths = append(paths, path)
		}
		fmt.Println(rpath, paths)
		shared, err := pcsconfig.Config.ActiveUserBaiduPCS().ShareSet(paths, nil)
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, shared.Link, "")
	}
}

func SettingHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	rmethod := r.Form.Get("method")
	pcsCommandVerbose.Info("设置:" + rmethod)

	config := pcsconfig.Config
	if rmethod == "get" {
		configJsons := make([]pcsConfigJSON, 0, 10)
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "PCS应用ID",
			EnName: "appid",
			Value:  strconv.Itoa(config.AppID()),
			Desc:   "",
		})
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "启用 https",
			EnName: "enable_https",
			Value:  fmt.Sprint(config.EnableHTTPS()),
			Desc:   "",
		})
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "浏览器标识",
			EnName: "user_agent",
			Value:  config.UserAgent(),
			Desc:   "",
		})
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "下载缓存",
			EnName: "cache_size",
			Value:  strconv.Itoa(config.CacheSize()),
			Desc:   "建议1024 ~ 262144, 如果硬盘占用高或下载速度慢, 请尝试调大此值",
		})
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "下载最大并发量",
			EnName: "max_parallel",
			Value:  strconv.Itoa(config.MaxParallel()),
			Desc:   "建议50 ~ 500. 单任务下载最大线程数量",
		})
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "同时下载数量",
			EnName: "max_download_load",
			Value:  strconv.Itoa(config.MaxDownloadLoad()),
			Desc:   "建议 1 ~ 5, 同时进行下载文件的最大数量",
		})
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "下载目录",
			EnName: "savedir",
			Value:  config.SaveDir(),
			Desc:   "下载文件的储存目录",
		})
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "工作目录",
			EnName: "workdir",
			Value:  pcsconfig.Config.ActiveUser().Workdir,
			Desc:   "程序启动时打开的目录，例如 /apps/baidu_shurufa",
		})
		envVar, ok := os.LookupEnv(pcsconfig.EnvConfigDir)
		if !ok {
			envVar = pcsconfig.GetConfigDir()
		}
		configJsons = append(configJsons, pcsConfigJSON{
			Name:   "配置文件目录",
			EnName: "config_dir",
			Value:  envVar,
			Desc:   "配置文件的储存目录，更改无效",
		})
		sendHttpResponse(w, "", configJsons)
	}
	if rmethod == "set" {
		appid := r.Form.Get("appid")
		enable_https := r.Form.Get("enable_https")
		user_agent := r.Form.Get("user_agent")
		cache_size := r.Form.Get("cache_size")
		max_parallel := r.Form.Get("max_parallel")
		max_download_load := r.Form.Get("max_download_load")
		savedir := r.Form.Get("savedir")
		_, err := ioutil.ReadDir(savedir)
		if err != nil {
			sendHttpErrorResponse(w, -1, "输入的文件夹路径错误")
			return
		}
		workdir := r.Form.Get("workdir")
		err = pcscommand.RunChangeDirectory(workdir, false)
		if err != nil {
			sendHttpErrorResponse(w, -1, "设置的百度云目录不存在，请检查")
			return
		}

		int_value, _ := strconv.Atoi(appid)
		config.SetAppID(int_value)
		bool_value, _ := strconv.ParseBool(enable_https)
		config.SetEnableHTTPS(bool_value)
		config.SetUserAgent(user_agent)
		int_value, _ = strconv.Atoi(cache_size)
		config.SetCacheSize(int_value)
		int_value, _ = strconv.Atoi(max_parallel)
		config.SetMaxParallel(int_value)
		int_value, _ = strconv.Atoi(max_download_load)
		config.SetMaxDownloadLoad(int_value)
		config.SetSaveDir(savedir)
		config.Save()
	}
	if rmethod == "update" {
		url := "http://www.zoranjojo.top:9925/api/v1/update?goos=" + runtime.GOOS + "&goarch=" + runtime.GOARCH + "&version=" + Version
		resp, err := http.Get(url)
		if err != nil {
			sendHttpErrorResponse(w, -1, "查找版本更新失败")
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)

		if err != nil {
			sendHttpErrorResponse(w, -2, "查找版本更新失败")
		}
		sendHttpResponse(w, "", string(body))
	}
	if rmethod == "notice" {
		url := "http://www.zoranjojo.top:9925/api/v1/notice"
		resp, err := http.Get(url)
		if err != nil {
			sendHttpErrorResponse(w, -1, "查找通知信息失败")
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)

		if err != nil {
			sendHttpErrorResponse(w, -2, "查找通知信息失败")
		}

		sendHttpResponse(w, "", string(body))
	}
}

func LogoutHandle(w http.ResponseWriter, r *http.Request) {
	activeUser := pcsconfig.Config.ActiveUser()
	deletedUser, err := pcsconfig.Config.DeleteUser(&pcsconfig.BaiduBase{
		UID: activeUser.UID,
	})
	if err != nil {
		fmt.Printf("退出用户 %s, 失败, 错误: %s\n", activeUser.Name, err)
	}

	fmt.Printf("退出用户成功, %s\n", deletedUser.Name)
	err = pcsconfig.Config.Save()
	if err != nil {
		fmt.Printf("保存配置错误: %s\n", err)
	}
	fmt.Printf("保存配置成功\n")
}

func LocalFileHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	rmethod := r.Form.Get("method")
	rpath := r.Form.Get("path")
	pcsCommandVerbose.Info("本地文件操作:" + rmethod + " " + rpath)

	if rmethod == "list" {
		files, err := ListLocalDir(rpath, "")
		if err != nil {
			sendHttpErrorResponse(w, -1, err.Error())
			return
		}
		sendHttpResponse(w, "", files)
		return
	}
	if rmethod == "open_folder" {
		tmp := strings.Split(rpath, "/")
		if runtime.GOOS == "windows" {
			path := strings.Join(tmp[:len(tmp)-1], "\\")
			cmd := exec.Command("explorer", path)
			cmd.Run()
			sendHttpResponse(w, "", "")
		} else if runtime.GOOS == "linux" {
			path := strings.Join(tmp[:len(tmp)-1], "/")
			cmd := exec.Command("nautilus", path)
			cmd.Run()
			sendHttpResponse(w, "", "")
		} else if runtime.GOOS == "darwin" {
			path := strings.Join(tmp[:len(tmp)-1], "/")
			cmd := exec.Command("open", path)
			cmd.Run()
			sendHttpResponse(w, "", "")
		} else {
			sendHttpErrorResponse(w, -1, "不支持的系统")
		}
		return
	}
}

func FileOperationHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	rmethod := r.Form.Get("method")
	rpaths := r.Form.Get("paths")
	pcsCommandVerbose.Info("远程文件操作:" + rmethod + " " + rpaths)

	paths := strings.Split(rpaths, "|")
	var err error
	if rmethod == "copy" {
		err = pcscommand.RunCopy(paths...)
	} else if rmethod == "move" {
		err = pcscommand.RunMove(paths...)
	} else if rmethod == "remove" {
		err = pcscommand.RunRemove(paths...)
	} else {
		sendHttpErrorResponse(w, -2, "方法调用错误")
	}
	if err != nil {
		sendHttpErrorResponse(w, -2, err.Error())
		return
	}
	sendHttpResponse(w, "success", "")
}

func MkdirHandle(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	rpath := r.Form.Get("path")
	pcsCommandVerbose.Info("远程新建文件夹:" + rpath)

	err := pcscommand.RunMkdir(rpath)
	if err != nil {
		sendHttpErrorResponse(w, -1, err.Error())
		return
	}
	sendHttpResponse(w, "success", "")
}

func fileList(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	fpath := r.Form.Get("path")
	orderBy := r.Form.Get("order_by")
	order := r.Form.Get("order")
	pcsCommandVerbose.Info("获取目录:" + fpath + " " + orderBy + " " + order)

	orderOptions := &baidupcs.OrderOptions{}
	switch {
	case order == "asc":
		orderOptions.Order = baidupcs.OrderAsc
	case order == "desc":
		orderOptions.Order = baidupcs.OrderDesc
	default:
		orderOptions.Order = baidupcs.OrderAsc
	}

	switch {
	case orderBy == "time":
		orderOptions.By = baidupcs.OrderByTime
	case orderBy == "name":
		orderOptions.By = baidupcs.OrderByName
	case orderBy == "size":
		orderOptions.By = baidupcs.OrderBySize
	default:
		orderOptions.By = baidupcs.OrderByName
	}

	dataReadCloser, err := pcsconfig.Config.ActiveUserBaiduPCS().PrepareFilesDirectoriesList(fpath, orderOptions)

	w.Header().Set("content-type", "application/json")

	if err != nil {
		sendHttpErrorResponse(w, -1, err.Error())
		return
	}

	defer dataReadCloser.Close()
	io.Copy(w, dataReadCloser)
}
