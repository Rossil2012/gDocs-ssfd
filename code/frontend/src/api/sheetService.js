import {getRequest, postRequest} from "./ajax";
import {HTTP_URL,MSG_WORDS} from "./common";
import {history} from "../route/history";
import {message} from "antd";

export const newSheet = () =>{
    const url = HTTP_URL+'newsheet';

    const token = JSON.parse(localStorage.getItem("token"));
    const name = "new sheet";

    const post_data = {
        token: token,
        name: name,
        initColumns:60,
        intiRows: 84,
    }
    console.log(post_data);

    const callback = (rec_data) => {
        console.log(rec_data);
        let msg_word = MSG_WORDS[rec_data.msg];
        if (rec_data.success === true) {
            history.push("/sheet?id=" + rec_data.data);
            let sheets = JSON.parse(localStorage.getItem('sheets'));
            sheets.push(
                {
                    fid:rec_data.data,
                    isDeleted:false,
                    name:"新建表格",
                    checkpoints:null,
                    columns:0,
                    content:null,
                }
            )
            localStorage.setItem("sheets",JSON.stringify(sheets))
            message.success(msg_word).then(r => {
            });
        } else {
            message.error(msg_word).then(r => {
            });
        }
    }
    postRequest(url, post_data, callback);
}

// need fid and token
export const getSheet = (data,callback) =>{
    const url = HTTP_URL+'getsheet';
    postRequest(url, data, callback);
}

// need fid and token
export const deleteSheet = (fid,callback) =>{
    const url = HTTP_URL+'deletesheet';
    const token = JSON.parse(localStorage.getItem("token"));
    const data={
        fid:fid,
        token:token,
    }

    postRequest(url, data, callback);
}


// need token fid chuck
export const getChuck = (data,callback) =>{
    const url = HTTP_URL+'getchunk';
    postRequest(url, data, callback);
}

export const testWS = (fid,callback) =>{
    const token = JSON.parse(localStorage.getItem("token"));
    const url = HTTP_URL + 'sheetws?token='+token+"&fid="+fid+"&query=1";
    getRequest(url,callback)
}
