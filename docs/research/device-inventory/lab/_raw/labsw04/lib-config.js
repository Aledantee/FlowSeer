var configArchLuton28  = 0;
var configArchJaguar_1 = 0;
var configArchLuton26  = 1;
var configBuildSMB     = 1;
var configBuildCE      = 0;
var configPortMin = 1;
var configNormalPortMax = 26;
var configRgmiiWifi = 0;
var configPortType = 0;
var configStackPortCount = 0;
var configStackPortMin = 27;
var configStackPortMax = 26;
var configSidMin = 1;
var configSidMax = 1;
var configAuthServerCnt = 0;
var configSwitchName = "GS-2326+";
var configSwitchDescription = "26-Port Layer-2 Managed Gigabit Ethernet Switch with 2x TP/SFP COMBO";

var configSoftwareId = 99;

var configHostNameLengthMax = 45;

var configSfpPortMin = 25;
var configSfpPortMax = 26;

function configPortName(portno, long) {
 var portname = String(portno);
 if(long) portname = "Port " + portname;
 return portname;
}

var configAccessMgmtMax = 16;
var configPolicyMax = 255;
var configAclEvcPolicerMin = 1;
var configAclEvcPolicerMax = 128;
var configPolicyBitmaskMax = 255;
var configAclRateLimitIdMax = 12;
var configAceMax = 256;
var configAclPktRateMax = 3276700;
var configAclBitRateMax = 1000000;
var configAclBitRateGranularity = 100;
function configHasAclEvcPolicer() {
  return 0;
}
var configLlagPortsMax = 16;
var configStaticGatewayMax = 4;
var configAuthServerCnt = 5;
function isDnsSupported() {
 return true;
}

var configPingLenMin = 2;
var configPingLenMax = 1452;
var configPingCntMin = 1;
var configPingCntMax = 60;
var configPingIntervalMin = 0;
var configPingIntervalMax = 30;
var configIpmcFilteringMax = 5;
var configIpmcVLANsMax = 32;
var configHasCDP = "1";
var configHasStpEnhancements = 1;
var configMVRAllowMax = 5;
var configPortFrameSizeMin = 1518;
var configPortFrameSizeMax = 9600;
var configPsecLimitLimitMax = 1024;
var configPvlanIdMin = 1;
var configPvlanIdMax = 26;
var configQosClassMax = 8;
var configQosBitRateMin = 100;
var configQosBitRateMax = 1000000;
var configQosBitRateDef = 500;
var configQosDplMax = 1;
var configQCLMax = 1;
var configQCEMax = 256;
var configQosDscpNames = ['0  (BE)','1','2','3','4','5','6','7','8  (CS1)','9','10 (AF11)','11','12 (AF12)','13','14 (AF13)','15','16 (CS2)','17','18 (AF21)','19','20 (AF22)','21','22 (AF23)','23','24 (CS3)','25','26 (AF31)','27','28 (AF32)','29','30 (AF33)','31','32 (CS4)','33','34 (AF41)','35','36 (AF42)','37','38 (AF43)','39','40 (CS5)','41','42','43','44','45','46 (EF)','47','48 (CS6)','49','50','51','52','53','54','55','56 (CS7)','57','58','59','60','61','62','63'];
var configQosTosNames = ['0 (Routine)','1 (Priority)','2 (Immediate)','3 (Flash)','4 (Flash Overdrive)','5 (Critical)','6 (Internetwork Control)','7 (Network Control)'];
function configIndexName(index, long) {
 var indexname = String(index);
 if(long) indexname = "ID " + indexname;
 return indexname;
}

var if_switch_interval = 1000;
var if_portid_start = 1;
var if_portid_end   = 27;
var if_llagid_start = 27;
var if_llagid_end = 40;
var if_glag_start = 16000;
var if_glag_max = 0;
var if_llag_cnt = 13;
var if_glag_cnt = 0;
var configUsernameMaxLen = 32;
var configPasswordMaxLen = 32;
var configHasIngressFiltering = 1;
var configVlanIdMin = 1;
var configVlanIdMax = 4095;
var configVoiceVlanOuiEntryCnt = 16;
