/*
Vitesse Switch Software.
Copyright (c) 2002-2011 Vitesse Semiconductor Corporation "Vitesse". All
Rights Reserved.
Unpublished rights reserved under the copyright laws of the United States of
America, other countries and international treaties. Permission to use, copy,
store and modify, the software and its source code is granted. Permission to
integrate into other products, disclose, transmit and distribute the software
in an absolute machine readable format (e.g. HEX file) is also granted.  The
source code of the software may not be disclosed, transmitted or distributed
without the written permission of Vitesse. The software and its source code
may only be used in products utilizing the Vitesse switch products.
This copyright notice must appear in any copy, modification, disclosure,
transmission or distribution of the software. Vitesse retains all ownership,
copyright, trade secret and proprietary rights in the software.
THIS SOFTWARE HAS BEEN PROVIDED "AS IS," WITHOUT EXPRESS OR IMPLIED WARRANTY
INCLUDING, WITHOUT LIMITATION, IMPLIED WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR USE AND NON-INFRINGEMENT.
*/
function initXMLHTTP() {
var req = null;
if(window.XMLHttpRequest) {
try {
req = new XMLHttpRequest();
} catch(e) {
req = null;
}
} else if(window.ActiveXObject) {
try {
req = new ActiveXObject("Msxml2.XMLHTTP");
} catch(e) {
try {
req = new ActiveXObject("Microsoft.XMLHTTP");
} catch(e) {
req = null;
}
}
}
return req;
}
function truncatString(valueString, token) {
var s1 = "";
var s2 = "";
var pos = 0;
if( (pos = valueString[0].indexOf(token))!=-1 ) {
s1 = valueString[0].substring(0, pos);
s2 = valueString[0].substring(pos+1, valueString[0].length);
valueString[0] = s1+s2;
truncatString(valueString, token);
}
}
function checkTxt()
{
var aStr = new Array();
aStr[0] = this.value;
if( (aStr[0].indexOf("<")!=-1) || (aStr[0].indexOf(">")!=-1) || (aStr[0].indexOf("\"")!=-1) ) {
truncatString(aStr, "<");
truncatString(aStr, ">");
truncatString(aStr, "\"");
}
this.value = aStr[0];
}
function changeForm(req)
{
var elem;
elem = document.getElementById("update");
if(elem) {
if(req == "grayOut") {
elem.style.visibility = "visible";
} else {
elem.style.visibility = "hidden";
}
}
elem = document.getElementsByTagName("input");
if(elem) {
for(var i = 0; i < elem.length; i++) {
if(elem[i].value == "Save" ||
elem[i].value == "Apply" ||
elem[i].value == "Activate Alternate Image" ||
elem[i].id == "addNewEntry" ||
elem[i].id == "addNewEntry1" ||
elem[i].value == "Next >" ||
elem[i].value == "Clear" ||
elem[i].value == "Clear All" ||
elem[i].value == "Clear This" ||
elem[i].value == "Remove All" ||
elem[i].value == "Delete User" ||
elem[i].value == "Start" ||
elem[i].value == "Yes" ||
elem[i].value == "No" ||
elem[i].name == "configuration" ||
elem[i].value == "Save configuration" ||
elem[i].value == "Save configuration to usb" ||
elem[i].value == "Translate dynamic to static" ||
elem[i].value == "Upload from USB" ||
elem[i].value == "Restore" ||        //@@Sherry,2014/01/16
elem[i].value == "Upload") {
if(elem[i].id!="tsapply"){   
if(req == "restore") {
elem[i].disabled = false;
} else {
elem[i].disabled = true;
}
}
}
if(elem[i].value == "Reset" ||
elem[i].id == "autorefresh" ||
elem[i].value == "Refresh" ||
elem[i].value == " << " ||
elem[i].value == " |<< " ||
elem[i].value == " >> " ||
elem[i].value == " >>| ") {
if(req == "grayOut") {
elem[i].disabled = true;
} else {
elem[i].disabled = false;
}
}
}
}
if(req == "readOnly") {
elem = document.getElementsByTagName("img");
if(elem) {
for(var j = 0; j < elem.length; j++) {
elem[j].onclick = null;
if (elem[j].title) {
elem[j].title = "You are not allowed to change settings";
}
}
}
}
}
function loadXMLDoc(file,callback,ref)
{
var req;
if ((req = initXMLHTTP()) == null) {
return null;
}
changeForm("grayOut");
if(typeof(configURLRemap) == "function") {
file = configURLRemap(file);
}
req.open("GET", file, true);
req.onreadystatechange = function () {
try {
if (req.readyState == 4) {
if (req.status && req.status == 200) {
if(req.getResponseHeader("X-ReadOnly") == "null") {
document.location.href = 'insuf_priv_lvl.htm';
} else {
var str = req.responseText;
if (str.indexOf("config/login") != -1) {
top.location.href = "/login.htm";
} else if (str.indexOf("MagicWordForIndex") != -1) {
top.location.href = "/";
} else if(str.indexOf("Insufficient Privilege Level")!=-1) {
document.location.href = 'insuf_priv_lvl.htm';
}else if(str.indexOf("Server disconnect")!=-1) {
document.location.href = 'tacplus_server_disconnect.htm';
}else{
if(typeof(callback) == "function") {
callback(req, ref);
}
if(req.getResponseHeader("X-ReadOnly") == "true") {
changeForm("readOnly");
} else {
changeForm("restore");
}
}
}
req = null;
} else {
try{
if(typeof(callback) == "function") {
callback(req, ref);
}
} catch(e) {
}
req = null;
}
}
}
catch(e){
req = null;
}
};
req.setRequestHeader("If-Modified-Since", "0");
req.send("");
return req;
}
function redirectOnErrorExtract(req, def_url)
{
var str = req.responseText;
var url = false;
if(req.responseText) {
if(str.match(/^Error:\s+/)) {
url = str.replace(/^Error:\s+/, "");
}
} else {
if(def_url) {
url = def_url;
}
}
return url;
}
function redirectOnError(req, def_url)
{
var url = redirectOnErrorExtract(req, def_url);
if(url) {
if(typeof(top.setErrorReferrer) == "function") {
top.setErrorReferrer(window.location.pathname);
}
window.location.pathname = url;
}
return url;
}
