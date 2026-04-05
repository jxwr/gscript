    0: 0xd10203ff  sub sp, sp, #0x80
    4: 0xa9007bfd  stp x29, x30, [sp]
    8: 0x910003fd  mov x29, sp
   12: 0xa90153f3  stp x19, x20, [sp,#16]
   16: 0xa9025bf5  stp x21, x22, [sp,#32]
   20: 0xa90363f7  stp x23, x24, [sp,#48]
   24: 0xa9046bf9  stp x25, x26, [sp,#64]
   28: 0xa90573fb  stp x27, x28, [sp,#80]
   32: 0x6d0627e8  stp d8, d9, [sp,#96]
   36: 0x6d072fea  stp d10, d11, [sp,#112]
   40: 0xaa0003f3  mov x19, x0
   44: 0xf940027a  ldr x26, [x19]
   48: 0xf940067b  ldr x27, [x19,#8]
   52: 0xd2ffffd8  mov x24, #0xfffe000000000000
   56: 0xd2ffffb9  mov x25, #0xfffd000000000000
   60: 0xf9400354  ldr x20, [x26]
   64: 0x92800000  mov x0, #0xffffffffffffffff
   68: 0xf900d260  str x0, [x19,#416]
   72: 0xf9000354  str x20, [x26]
   76: 0xd2800320  mov x0, #0x19
   80: 0xf9002260  str x0, [x19,#64]
   84: 0xd2800000  mov x0, #0x0
   88: 0xf9002660  str x0, [x19,#72]
   92: 0xd2800020  mov x0, #0x1
   96: 0xf9002a60  str x0, [x19,#80]
  100: 0xd2800080  mov x0, #0x4
  104: 0xf9000a60  str x0, [x19,#16]
  108: 0x14000949  b .+0x2524
  112: 0xf9400354  ldr x20, [x26]
  116: 0xf9406740  ldr x0, [x26,#200]
  120: 0xaa0003f5  mov x21, x0
  124: 0xf9000354  str x20, [x26]
  128: 0xf9006755  str x21, [x26,#200]
  132: 0xd28001e0  mov x0, #0xf
  136: 0xf9004a60  str x0, [x19,#144]
  140: 0xd2800340  mov x0, #0x1a
  144: 0xf9004e60  str x0, [x19,#152]
  148: 0xd2800320  mov x0, #0x19
  152: 0xf9005260  str x0, [x19,#160]
  156: 0xd2800000  mov x0, #0x0
  160: 0xf9005660  str x0, [x19,#168]
  164: 0xd2800000  mov x0, #0x0
  168: 0xf9005a60  str x0, [x19,#176]
  172: 0xd2800040  mov x0, #0x2
  176: 0xf9005e60  str x0, [x19,#184]
  180: 0xd28000c0  mov x0, #0x6
  184: 0xf9000a60  str x0, [x19,#16]
  188: 0x14000935  b .+0x24d4
  192: 0xf9400354  ldr x20, [x26]
  196: 0xf9406755  ldr x21, [x26,#200]
  200: 0xf9406b40  ldr x0, [x26,#208]
  204: 0xaa0003f6  mov x22, x0
  208: 0xf9006b56  str x22, [x26,#208]
  212: 0xd2800035  mov x21, #0x1
  216: 0xd340bea0  ubfx x0, x21, #0, #48
  220: 0xaa180000  orr x0, x0, x24
  224: 0xf9006f40  str x0, [x26,#216]
  228: 0xd2800017  mov x23, #0x0
  232: 0xd340bee0  ubfx x0, x23, #0, #48
  236: 0xaa180000  orr x0, x0, x24
  240: 0xf9007340  str x0, [x26,#224]
  244: 0xaa1703f4  mov x20, x23
  248: 0x14000708  b .+0x1c20
  252: 0x92800000  mov x0, #0xffffffffffffffff
  256: 0xf900d260  str x0, [x19,#416]
  260: 0xd28003a0  mov x0, #0x1d
  264: 0xf9002260  str x0, [x19,#64]
  268: 0xd2800000  mov x0, #0x0
  272: 0xf9002660  str x0, [x19,#72]
  276: 0xd28000e0  mov x0, #0x7
  280: 0xf9002a60  str x0, [x19,#80]
  284: 0xd2800080  mov x0, #0x4
  288: 0xf9000a60  str x0, [x19,#16]
  292: 0x1400091b  b .+0x246c
  296: 0xf9407740  ldr x0, [x26,#232]
  300: 0xaa0003f4  mov x20, x0
  304: 0xaa1403e0  mov x0, x20
  308: 0xd370fc01  lsr x1, x0, #48
  312: 0xd29fffe2  mov x2, #0xffff
  316: 0xeb02003f  cmp x1, x2
  320: 0x54000501  b.ne .+0xa0
  324: 0xd36cfc01  lsr x1, x0, #44
  328: 0xd28001e2  mov x2, #0xf
  332: 0x8a020021  and x1, x1, x2
  336: 0xf100003f  cmp x1, #0x0
  340: 0x54000461  b.ne .+0x8c
  344: 0xd340ac00  ubfx x0, x0, #0, #44
  348: 0xb4000420  cbz x0, .+0x84
  352: 0xf9403401  ldr x1, [x0,#104]
  356: 0xb50003e1  cbnz x1, .+0x7c
  360: 0xf9418341  ldr x1, [x26,#768]
  364: 0xd370fc22  lsr x2, x1, #48
  368: 0xd29fffc3  mov x3, #0xfffe
  372: 0xeb03005f  cmp x2, x3
  376: 0x54000341  b.ne .+0x68
  380: 0x9340bc21  sbfx x1, x1, #0, #48
  384: 0xf100003f  cmp x1, #0x0
  388: 0x540002eb  b.lt .+0x5c
  392: 0x39422402  ldrb w2, [x0,#137]
  396: 0xf100045f  cmp x2, #0x1
  400: 0x54000140  b.eq .+0x28
  404: 0xb5000262  cbnz x2, .+0x4c
  408: 0xf9400802  ldr x2, [x0,#16]
  412: 0xeb02003f  cmp x1, x2
  416: 0x5400020a  b.ge .+0x40
  420: 0xf9400402  ldr x2, [x0,#8]
  424: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  428: 0xaa0003f5  mov x21, x0
  432: 0xf9007b55  str x21, [x26,#240]
  436: 0x14000023  b .+0x8c
  440: 0xf9404c02  ldr x2, [x0,#152]
  444: 0xeb02003f  cmp x1, x2
  448: 0x5400010a  b.ge .+0x20
  452: 0xf9404802  ldr x2, [x0,#144]
  456: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  460: 0xd340bc00  ubfx x0, x0, #0, #48
  464: 0xaa180000  orr x0, x0, x24
  468: 0xaa0003f5  mov x21, x0
  472: 0xf9007b55  str x21, [x26,#240]
  476: 0x14000019  b .+0x64
  480: 0xaa1403e0  mov x0, x20
  484: 0xf9007740  str x0, [x26,#232]
  488: 0xf9418340  ldr x0, [x26,#768]
  492: 0xf9018340  str x0, [x26,#768]
  496: 0xf9007b55  str x21, [x26,#240]
  500: 0xf9007754  str x20, [x26,#232]
  504: 0xd2800020  mov x0, #0x1
  508: 0xf9002e60  str x0, [x19,#88]
  512: 0xd28003a0  mov x0, #0x1d
  516: 0xf9003260  str x0, [x19,#96]
  520: 0xd2800c00  mov x0, #0x60
  524: 0xf9003660  str x0, [x19,#104]
  528: 0xd28003c0  mov x0, #0x1e
  532: 0xf9003e60  str x0, [x19,#120]
  536: 0xd2800120  mov x0, #0x9
  540: 0xf9004660  str x0, [x19,#136]
  544: 0xd28000a0  mov x0, #0x5
  548: 0xf9000a60  str x0, [x19,#16]
  552: 0x140008da  b .+0x2368
  556: 0xf9407754  ldr x20, [x26,#232]
  560: 0xf9407b55  ldr x21, [x26,#240]
  564: 0xf9407b40  ldr x0, [x26,#240]
  568: 0xaa0003f5  mov x21, x0
  572: 0xf9007b55  str x21, [x26,#240]
  576: 0xd2800034  mov x20, #0x1
  580: 0xf9418340  ldr x0, [x26,#768]
  584: 0x9340bc00  sbfx x0, x0, #0, #48
  588: 0x91000416  add x22, x0, #0x1
  592: 0x9340bec0  sbfx x0, x22, #0, #48
  596: 0xeb16001f  cmp x0, x22
  600: 0x54000100  b.eq .+0x20
  604: 0xf9007b55  str x21, [x26,#240]
  608: 0xd340be80  ubfx x0, x20, #0, #48
  612: 0xaa180000  orr x0, x0, x24
  616: 0xf9007f40  str x0, [x26,#248]
  620: 0xd2800040  mov x0, #0x2
  624: 0xf9000a60  str x0, [x19,#16]
  628: 0x140008c7  b .+0x231c
  632: 0xd2800034  mov x20, #0x1
  636: 0xd340be80  ubfx x0, x20, #0, #48
  640: 0xaa180000  orr x0, x0, x24
  644: 0xf9008740  str x0, [x26,#264]
  648: 0xd10006d7  sub x23, x22, #0x1
  652: 0xaa1703f4  mov x20, x23
  656: 0x14000693  b .+0x1a4c
  660: 0x92800000  mov x0, #0xffffffffffffffff
  664: 0xf900d260  str x0, [x19,#416]
  668: 0xd2800460  mov x0, #0x23
  672: 0xf9002260  str x0, [x19,#64]
  676: 0xd2800000  mov x0, #0x0
  680: 0xf9002660  str x0, [x19,#72]
  684: 0xd2800200  mov x0, #0x10
  688: 0xf9002a60  str x0, [x19,#80]
  692: 0xd2800080  mov x0, #0x4
  696: 0xf9000a60  str x0, [x19,#16]
  700: 0x140008b5  b .+0x22d4
  704: 0xf9408f40  ldr x0, [x26,#280]
  708: 0xaa0003f4  mov x20, x0
  712: 0xaa1403e0  mov x0, x20
  716: 0xd370fc01  lsr x1, x0, #48
  720: 0xd29fffe2  mov x2, #0xffff
  724: 0xeb02003f  cmp x1, x2
  728: 0x540004c1  b.ne .+0x98
  732: 0xd36cfc01  lsr x1, x0, #44
  736: 0xd28001e2  mov x2, #0xf
  740: 0x8a020021  and x1, x1, x2
  744: 0xf100003f  cmp x1, #0x0
  748: 0x54000421  b.ne .+0x84
  752: 0xd340ac00  ubfx x0, x0, #0, #44
  756: 0xb40003e0  cbz x0, .+0x7c
  760: 0xf9403401  ldr x1, [x0,#104]
  764: 0xb50003a1  cbnz x1, .+0x74
  768: 0xf9417741  ldr x1, [x26,#744]
  772: 0xd370fc22  lsr x2, x1, #48
  776: 0xd29fffc3  mov x3, #0xfffe
  780: 0xeb03005f  cmp x2, x3
  784: 0x54000301  b.ne .+0x60
  788: 0x9340bc21  sbfx x1, x1, #0, #48
  792: 0xf100003f  cmp x1, #0x0
  796: 0x540002ab  b.lt .+0x54
  800: 0x39422402  ldrb w2, [x0,#137]
  804: 0xf100045f  cmp x2, #0x1
  808: 0x54000120  b.eq .+0x24
  812: 0xb5000222  cbnz x2, .+0x44
  816: 0xf9400802  ldr x2, [x0,#16]
  820: 0xeb02003f  cmp x1, x2
  824: 0x540001ca  b.ge .+0x38
  828: 0xf9400402  ldr x2, [x0,#8]
  832: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  836: 0xf9009340  str x0, [x26,#288]
  840: 0x1400001f  b .+0x7c
  844: 0xf9404c02  ldr x2, [x0,#152]
  848: 0xeb02003f  cmp x1, x2
  852: 0x540000ea  b.ge .+0x1c
  856: 0xf9404802  ldr x2, [x0,#144]
  860: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  864: 0xd340bc00  ubfx x0, x0, #0, #48
  868: 0xaa180000  orr x0, x0, x24
  872: 0xf9009340  str x0, [x26,#288]
  876: 0x14000016  b .+0x58
  880: 0xaa1403e0  mov x0, x20
  884: 0xf9008f40  str x0, [x26,#280]
  888: 0xf9417740  ldr x0, [x26,#744]
  892: 0xf9017740  str x0, [x26,#744]
  896: 0xf9008f54  str x20, [x26,#280]
  900: 0xd2800020  mov x0, #0x1
  904: 0xf9002e60  str x0, [x19,#88]
  908: 0xd2800460  mov x0, #0x23
  912: 0xf9003260  str x0, [x19,#96]
  916: 0xd2800ba0  mov x0, #0x5d
  920: 0xf9003660  str x0, [x19,#104]
  924: 0xd2800480  mov x0, #0x24
  928: 0xf9003e60  str x0, [x19,#120]
  932: 0xd2800240  mov x0, #0x12
  936: 0xf9004660  str x0, [x19,#136]
  940: 0xd28000a0  mov x0, #0x5
  944: 0xf9000a60  str x0, [x19,#16]
  948: 0x14000877  b .+0x21dc
  952: 0xf9408f54  ldr x20, [x26,#280]
  956: 0xf9409340  ldr x0, [x26,#288]
  960: 0xf9009340  str x0, [x26,#288]
  964: 0xf9407b40  ldr x0, [x26,#240]
  968: 0xf9007b40  str x0, [x26,#240]
  972: 0xf9008f54  str x20, [x26,#280]
  976: 0xd2800060  mov x0, #0x3
  980: 0xf9002e60  str x0, [x19,#88]
  984: 0xd28003c0  mov x0, #0x1e
  988: 0xf9003260  str x0, [x19,#96]
  992: 0xd2800020  mov x0, #0x1
  996: 0xf9003e60  str x0, [x19,#120]
 1000: 0xd28004a0  mov x0, #0x25
 1004: 0xf9004260  str x0, [x19,#128]
 1008: 0xd2800280  mov x0, #0x14
 1012: 0xf9004660  str x0, [x19,#136]
 1016: 0xd28000a0  mov x0, #0x5
 1020: 0xf9000a60  str x0, [x19,#16]
 1024: 0x14000864  b .+0x2190
 1028: 0xf9408f54  ldr x20, [x26,#280]
 1032: 0xf9409740  ldr x0, [x26,#296]
 1036: 0xaa0003f4  mov x20, x0
 1040: 0xf9409340  ldr x0, [x26,#288]
 1044: 0xf9009340  str x0, [x26,#288]
 1048: 0xf9009754  str x20, [x26,#296]
 1052: 0xd2800060  mov x0, #0x3
 1056: 0xf9002e60  str x0, [x19,#88]
 1060: 0xd2800480  mov x0, #0x24
 1064: 0xf9003260  str x0, [x19,#96]
 1068: 0xd2800020  mov x0, #0x1
 1072: 0xf9003e60  str x0, [x19,#120]
 1076: 0xd28004c0  mov x0, #0x26
 1080: 0xf9004260  str x0, [x19,#128]
 1084: 0xd28002a0  mov x0, #0x15
 1088: 0xf9004660  str x0, [x19,#136]
 1092: 0xd28000a0  mov x0, #0x5
 1096: 0xf9000a60  str x0, [x19,#16]
 1100: 0x14000851  b .+0x2144
 1104: 0xf9409754  ldr x20, [x26,#296]
 1108: 0xf9409b40  ldr x0, [x26,#304]
 1112: 0xaa0003f6  mov x22, x0
 1116: 0xaa1403e0  mov x0, x20
 1120: 0xaa1603e1  mov x1, x22
 1124: 0xd370fc02  lsr x2, x0, #48
 1128: 0xd29fffc3  mov x3, #0xfffe
 1132: 0xeb03005f  cmp x2, x3
 1136: 0x54000161  b.ne .+0x2c
 1140: 0xd370fc22  lsr x2, x1, #48
 1144: 0xd29fffc3  mov x3, #0xfffe
 1148: 0xeb03005f  cmp x2, x3
 1152: 0x540001e1  b.ne .+0x3c
 1156: 0x9340bc00  sbfx x0, x0, #0, #48
 1160: 0x9340bc21  sbfx x1, x1, #0, #48
 1164: 0xcb010000  sub x0, x0, x1
 1168: 0xd340bc00  ubfx x0, x0, #0, #48
 1172: 0xaa180000  orr x0, x0, x24
 1176: 0x14000010  b .+0x40
 1180: 0x9e670000  fmov d0, x0
 1184: 0xd370fc22  lsr x2, x1, #48
 1188: 0xd29fffc3  mov x3, #0xfffe
 1192: 0xeb03005f  cmp x2, x3
 1196: 0x54000101  b.ne .+0x20
 1200: 0x9340bc21  sbfx x1, x1, #0, #48
 1204: 0x9e620021  scvtf d1, x1
 1208: 0x14000006  b .+0x18
 1212: 0x9340bc00  sbfx x0, x0, #0, #48
 1216: 0x9e620000  scvtf d0, x0
 1220: 0x9e670021  fmov d1, x1
 1224: 0x14000002  b .+0x8
 1228: 0x9e670021  fmov d1, x1
 1232: 0x1e613800  fsub d0, d0, d1
 1236: 0x9e660000  fmov x0, d0
 1240: 0xf9009f40  str x0, [x26,#312]
 1244: 0xf9407b40  ldr x0, [x26,#240]
 1248: 0xf9007b40  str x0, [x26,#240]
 1252: 0xf9009754  str x20, [x26,#296]
 1256: 0xf9009b56  str x22, [x26,#304]
 1260: 0xd2800060  mov x0, #0x3
 1264: 0xf9002e60  str x0, [x19,#88]
 1268: 0xd28003c0  mov x0, #0x1e
 1272: 0xf9003260  str x0, [x19,#96]
 1276: 0xd2800040  mov x0, #0x2
 1280: 0xf9003e60  str x0, [x19,#120]
 1284: 0xd2800500  mov x0, #0x28
 1288: 0xf9004260  str x0, [x19,#128]
 1292: 0xd28002e0  mov x0, #0x17
 1296: 0xf9004660  str x0, [x19,#136]
 1300: 0xd28000a0  mov x0, #0x5
 1304: 0xf9000a60  str x0, [x19,#16]
 1308: 0x1400081d  b .+0x2074
 1312: 0xf9409754  ldr x20, [x26,#296]
 1316: 0xf9409b56  ldr x22, [x26,#304]
 1320: 0xf940a340  ldr x0, [x26,#320]
 1324: 0xaa0003f4  mov x20, x0
 1328: 0xf9409340  ldr x0, [x26,#288]
 1332: 0xf9009340  str x0, [x26,#288]
 1336: 0xf900a354  str x20, [x26,#320]
 1340: 0xf9009b56  str x22, [x26,#304]
 1344: 0xd2800060  mov x0, #0x3
 1348: 0xf9002e60  str x0, [x19,#88]
 1352: 0xd2800480  mov x0, #0x24
 1356: 0xf9003260  str x0, [x19,#96]
 1360: 0xd2800040  mov x0, #0x2
 1364: 0xf9003e60  str x0, [x19,#120]
 1368: 0xd2800520  mov x0, #0x29
 1372: 0xf9004260  str x0, [x19,#128]
 1376: 0xd2800300  mov x0, #0x18
 1380: 0xf9004660  str x0, [x19,#136]
 1384: 0xd28000a0  mov x0, #0x5
 1388: 0xf9000a60  str x0, [x19,#16]
 1392: 0x14000808  b .+0x2020
 1396: 0xf940a354  ldr x20, [x26,#320]
 1400: 0xf9409b56  ldr x22, [x26,#304]
 1404: 0xf940a740  ldr x0, [x26,#328]
 1408: 0xaa0003f6  mov x22, x0
 1412: 0xaa1403e0  mov x0, x20
 1416: 0xaa1603e1  mov x1, x22
 1420: 0xd370fc02  lsr x2, x0, #48
 1424: 0xd29fffc3  mov x3, #0xfffe
 1428: 0xeb03005f  cmp x2, x3
 1432: 0x54000161  b.ne .+0x2c
 1436: 0xd370fc22  lsr x2, x1, #48
 1440: 0xd29fffc3  mov x3, #0xfffe
 1444: 0xeb03005f  cmp x2, x3
 1448: 0x540001e1  b.ne .+0x3c
 1452: 0x9340bc00  sbfx x0, x0, #0, #48
 1456: 0x9340bc21  sbfx x1, x1, #0, #48
 1460: 0xcb010000  sub x0, x0, x1
 1464: 0xd340bc00  ubfx x0, x0, #0, #48
 1468: 0xaa180000  orr x0, x0, x24
 1472: 0x14000010  b .+0x40
 1476: 0x9e670000  fmov d0, x0
 1480: 0xd370fc22  lsr x2, x1, #48
 1484: 0xd29fffc3  mov x3, #0xfffe
 1488: 0xeb03005f  cmp x2, x3
 1492: 0x54000101  b.ne .+0x20
 1496: 0x9340bc21  sbfx x1, x1, #0, #48
 1500: 0x9e620021  scvtf d1, x1
 1504: 0x14000006  b .+0x18
 1508: 0x9340bc00  sbfx x0, x0, #0, #48
 1512: 0x9e620000  scvtf d0, x0
 1516: 0x9e670021  fmov d1, x1
 1520: 0x14000002  b .+0x8
 1524: 0x9e670021  fmov d1, x1
 1528: 0x1e613800  fsub d0, d0, d1
 1532: 0x9e660000  fmov x0, d0
 1536: 0xf900ab40  str x0, [x26,#336]
 1540: 0xf9407b40  ldr x0, [x26,#240]
 1544: 0xf9007b40  str x0, [x26,#240]
 1548: 0xf900a354  str x20, [x26,#320]
 1552: 0xf900a756  str x22, [x26,#328]
 1556: 0xd2800060  mov x0, #0x3
 1560: 0xf9002e60  str x0, [x19,#88]
 1564: 0xd28003c0  mov x0, #0x1e
 1568: 0xf9003260  str x0, [x19,#96]
 1572: 0xd2800060  mov x0, #0x3
 1576: 0xf9003e60  str x0, [x19,#120]
 1580: 0xd2800560  mov x0, #0x2b
 1584: 0xf9004260  str x0, [x19,#128]
 1588: 0xd2800340  mov x0, #0x1a
 1592: 0xf9004660  str x0, [x19,#136]
 1596: 0xd28000a0  mov x0, #0x5
 1600: 0xf9000a60  str x0, [x19,#16]
 1604: 0x140007d3  b .+0x1f4c
 1608: 0xf940a756  ldr x22, [x26,#328]
 1612: 0xf940a354  ldr x20, [x26,#320]
 1616: 0xf940af40  ldr x0, [x26,#344]
 1620: 0xaa0003f4  mov x20, x0
 1624: 0xf9409340  ldr x0, [x26,#288]
 1628: 0xf9009340  str x0, [x26,#288]
 1632: 0xf900af54  str x20, [x26,#344]
 1636: 0xf900a756  str x22, [x26,#328]
 1640: 0xd2800060  mov x0, #0x3
 1644: 0xf9002e60  str x0, [x19,#88]
 1648: 0xd2800480  mov x0, #0x24
 1652: 0xf9003260  str x0, [x19,#96]
 1656: 0xd2800060  mov x0, #0x3
 1660: 0xf9003e60  str x0, [x19,#120]
 1664: 0xd2800580  mov x0, #0x2c
 1668: 0xf9004260  str x0, [x19,#128]
 1672: 0xd2800360  mov x0, #0x1b
 1676: 0xf9004660  str x0, [x19,#136]
 1680: 0xd28000a0  mov x0, #0x5
 1684: 0xf9000a60  str x0, [x19,#16]
 1688: 0x140007be  b .+0x1ef8
 1692: 0xf940a756  ldr x22, [x26,#328]
 1696: 0xf940af54  ldr x20, [x26,#344]
 1700: 0xf940b340  ldr x0, [x26,#352]
 1704: 0xaa0003f6  mov x22, x0
 1708: 0xaa1403e0  mov x0, x20
 1712: 0xaa1603e1  mov x1, x22
 1716: 0xd370fc02  lsr x2, x0, #48
 1720: 0xd29fffc3  mov x3, #0xfffe
 1724: 0xeb03005f  cmp x2, x3
 1728: 0x54000161  b.ne .+0x2c
 1732: 0xd370fc22  lsr x2, x1, #48
 1736: 0xd29fffc3  mov x3, #0xfffe
 1740: 0xeb03005f  cmp x2, x3
 1744: 0x540001e1  b.ne .+0x3c
 1748: 0x9340bc00  sbfx x0, x0, #0, #48
 1752: 0x9340bc21  sbfx x1, x1, #0, #48
 1756: 0xcb010000  sub x0, x0, x1
 1760: 0xd340bc00  ubfx x0, x0, #0, #48
 1764: 0xaa180000  orr x0, x0, x24
 1768: 0x14000010  b .+0x40
 1772: 0x9e670000  fmov d0, x0
 1776: 0xd370fc22  lsr x2, x1, #48
 1780: 0xd29fffc3  mov x3, #0xfffe
 1784: 0xeb03005f  cmp x2, x3
 1788: 0x54000101  b.ne .+0x20
 1792: 0x9340bc21  sbfx x1, x1, #0, #48
 1796: 0x9e620021  scvtf d1, x1
 1800: 0x14000006  b .+0x18
 1804: 0x9340bc00  sbfx x0, x0, #0, #48
 1808: 0x9e620000  scvtf d0, x0
 1812: 0x9e670021  fmov d1, x1
 1816: 0x14000002  b .+0x8
 1820: 0x9e670021  fmov d1, x1
 1824: 0x1e613800  fsub d0, d0, d1
 1828: 0x9e660000  fmov x0, d0
 1832: 0xf900b740  str x0, [x26,#360]
 1836: 0xf9409f40  ldr x0, [x26,#312]
 1840: 0xf9409f41  ldr x1, [x26,#312]
 1844: 0xd370fc02  lsr x2, x0, #48
 1848: 0xd29fffc3  mov x3, #0xfffe
 1852: 0xeb03005f  cmp x2, x3
 1856: 0x54000161  b.ne .+0x2c
 1860: 0xd370fc22  lsr x2, x1, #48
 1864: 0xd29fffc3  mov x3, #0xfffe
 1868: 0xeb03005f  cmp x2, x3
 1872: 0x540001e1  b.ne .+0x3c
 1876: 0x9340bc00  sbfx x0, x0, #0, #48
 1880: 0x9340bc21  sbfx x1, x1, #0, #48
 1884: 0x9b017c00  mul x0, x0, x1
 1888: 0xd340bc00  ubfx x0, x0, #0, #48
 1892: 0xaa180000  orr x0, x0, x24
 1896: 0x14000010  b .+0x40
 1900: 0x9e670000  fmov d0, x0
 1904: 0xd370fc22  lsr x2, x1, #48
 1908: 0xd29fffc3  mov x3, #0xfffe
 1912: 0xeb03005f  cmp x2, x3
 1916: 0x54000101  b.ne .+0x20
 1920: 0x9340bc21  sbfx x1, x1, #0, #48
 1924: 0x9e620021  scvtf d1, x1
 1928: 0x14000006  b .+0x18
 1932: 0x9340bc00  sbfx x0, x0, #0, #48
 1936: 0x9e620000  scvtf d0, x0
 1940: 0x9e670021  fmov d1, x1
 1944: 0x14000002  b .+0x8
 1948: 0x9e670021  fmov d1, x1
 1952: 0x1e610800  fmul d0, d0, d1
 1956: 0x9e660000  fmov x0, d0
 1960: 0xaa0003f4  mov x20, x0
 1964: 0xf940ab40  ldr x0, [x26,#336]
 1968: 0xf940ab41  ldr x1, [x26,#336]
 1972: 0xd370fc02  lsr x2, x0, #48
 1976: 0xd29fffc3  mov x3, #0xfffe
 1980: 0xeb03005f  cmp x2, x3
 1984: 0x54000161  b.ne .+0x2c
 1988: 0xd370fc22  lsr x2, x1, #48
 1992: 0xd29fffc3  mov x3, #0xfffe
 1996: 0xeb03005f  cmp x2, x3
 2000: 0x540001e1  b.ne .+0x3c
 2004: 0x9340bc00  sbfx x0, x0, #0, #48
 2008: 0x9340bc21  sbfx x1, x1, #0, #48
 2012: 0x9b017c00  mul x0, x0, x1
 2016: 0xd340bc00  ubfx x0, x0, #0, #48
 2020: 0xaa180000  orr x0, x0, x24
 2024: 0x14000010  b .+0x40
 2028: 0x9e670000  fmov d0, x0
 2032: 0xd370fc22  lsr x2, x1, #48
 2036: 0xd29fffc3  mov x3, #0xfffe
 2040: 0xeb03005f  cmp x2, x3
 2044: 0x54000101  b.ne .+0x20
 2048: 0x9340bc21  sbfx x1, x1, #0, #48
 2052: 0x9e620021  scvtf d1, x1
 2056: 0x14000006  b .+0x18
 2060: 0x9340bc00  sbfx x0, x0, #0, #48
 2064: 0x9e620000  scvtf d0, x0
 2068: 0x9e670021  fmov d1, x1
 2072: 0x14000002  b .+0x8
 2076: 0x9e670021  fmov d1, x1
 2080: 0x1e610800  fmul d0, d0, d1
 2084: 0x9e660000  fmov x0, d0
 2088: 0xaa0003f6  mov x22, x0
 2092: 0xaa1403e0  mov x0, x20
 2096: 0xaa1603e1  mov x1, x22
 2100: 0xd370fc02  lsr x2, x0, #48
 2104: 0xd29fffc3  mov x3, #0xfffe
 2108: 0xeb03005f  cmp x2, x3
 2112: 0x54000161  b.ne .+0x2c
 2116: 0xd370fc22  lsr x2, x1, #48
 2120: 0xd29fffc3  mov x3, #0xfffe
 2124: 0xeb03005f  cmp x2, x3
 2128: 0x540001e1  b.ne .+0x3c
 2132: 0x9340bc00  sbfx x0, x0, #0, #48
 2136: 0x9340bc21  sbfx x1, x1, #0, #48
 2140: 0x8b010000  add x0, x0, x1
 2144: 0xd340bc00  ubfx x0, x0, #0, #48
 2148: 0xaa180000  orr x0, x0, x24
 2152: 0x14000010  b .+0x40
 2156: 0x9e670000  fmov d0, x0
 2160: 0xd370fc22  lsr x2, x1, #48
 2164: 0xd29fffc3  mov x3, #0xfffe
 2168: 0xeb03005f  cmp x2, x3
 2172: 0x54000101  b.ne .+0x20
 2176: 0x9340bc21  sbfx x1, x1, #0, #48
 2180: 0x9e620021  scvtf d1, x1
 2184: 0x14000006  b .+0x18
 2188: 0x9340bc00  sbfx x0, x0, #0, #48
 2192: 0x9e620000  scvtf d0, x0
 2196: 0x9e670021  fmov d1, x1
 2200: 0x14000002  b .+0x8
 2204: 0x9e670021  fmov d1, x1
 2208: 0x1e612800  fadd d0, d0, d1
 2212: 0x9e660000  fmov x0, d0
 2216: 0xaa0003f5  mov x21, x0
 2220: 0xf940b740  ldr x0, [x26,#360]
 2224: 0xf940b741  ldr x1, [x26,#360]
 2228: 0xd370fc02  lsr x2, x0, #48
 2232: 0xd29fffc3  mov x3, #0xfffe
 2236: 0xeb03005f  cmp x2, x3
 2240: 0x54000161  b.ne .+0x2c
 2244: 0xd370fc22  lsr x2, x1, #48
 2248: 0xd29fffc3  mov x3, #0xfffe
 2252: 0xeb03005f  cmp x2, x3
 2256: 0x540001e1  b.ne .+0x3c
 2260: 0x9340bc00  sbfx x0, x0, #0, #48
 2264: 0x9340bc21  sbfx x1, x1, #0, #48
 2268: 0x9b017c00  mul x0, x0, x1
 2272: 0xd340bc00  ubfx x0, x0, #0, #48
 2276: 0xaa180000  orr x0, x0, x24
 2280: 0x14000010  b .+0x40
 2284: 0x9e670000  fmov d0, x0
 2288: 0xd370fc22  lsr x2, x1, #48
 2292: 0xd29fffc3  mov x3, #0xfffe
 2296: 0xeb03005f  cmp x2, x3
 2300: 0x54000101  b.ne .+0x20
 2304: 0x9340bc21  sbfx x1, x1, #0, #48
 2308: 0x9e620021  scvtf d1, x1
 2312: 0x14000006  b .+0x18
 2316: 0x9340bc00  sbfx x0, x0, #0, #48
 2320: 0x9e620000  scvtf d0, x0
 2324: 0x9e670021  fmov d1, x1
 2328: 0x14000002  b .+0x8
 2332: 0x9e670021  fmov d1, x1
 2336: 0x1e610800  fmul d0, d0, d1
 2340: 0x9e660000  fmov x0, d0
 2344: 0xaa0003f4  mov x20, x0
 2348: 0xaa1503e0  mov x0, x21
 2352: 0xaa1403e1  mov x1, x20
 2356: 0xd370fc02  lsr x2, x0, #48
 2360: 0xd29fffc3  mov x3, #0xfffe
 2364: 0xeb03005f  cmp x2, x3
 2368: 0x54000161  b.ne .+0x2c
 2372: 0xd370fc22  lsr x2, x1, #48
 2376: 0xd29fffc3  mov x3, #0xfffe
 2380: 0xeb03005f  cmp x2, x3
 2384: 0x540001e1  b.ne .+0x3c
 2388: 0x9340bc00  sbfx x0, x0, #0, #48
 2392: 0x9340bc21  sbfx x1, x1, #0, #48
 2396: 0x8b010000  add x0, x0, x1
 2400: 0xd340bc00  ubfx x0, x0, #0, #48
 2404: 0xaa180000  orr x0, x0, x24
 2408: 0x14000010  b .+0x40
 2412: 0x9e670000  fmov d0, x0
 2416: 0xd370fc22  lsr x2, x1, #48
 2420: 0xd29fffc3  mov x3, #0xfffe
 2424: 0xeb03005f  cmp x2, x3
 2428: 0x54000101  b.ne .+0x20
 2432: 0x9340bc21  sbfx x1, x1, #0, #48
 2436: 0x9e620021  scvtf d1, x1
 2440: 0x14000006  b .+0x18
 2444: 0x9340bc00  sbfx x0, x0, #0, #48
 2448: 0x9e620000  scvtf d0, x0
 2452: 0x9e670021  fmov d1, x1
 2456: 0x14000002  b .+0x8
 2460: 0x9e670021  fmov d1, x1
 2464: 0x1e612800  fadd d0, d0, d1
 2468: 0x9e660000  fmov x0, d0
 2472: 0xaa0003f6  mov x22, x0
 2476: 0x92800000  mov x0, #0xffffffffffffffff
 2480: 0xf900d260  str x0, [x19,#416]
 2484: 0xf900c355  str x21, [x26,#384]
 2488: 0xf900c754  str x20, [x26,#392]
 2492: 0xf900cb56  str x22, [x26,#400]
 2496: 0xd2800660  mov x0, #0x33
 2500: 0xf9002260  str x0, [x19,#64]
 2504: 0xd2800080  mov x0, #0x4
 2508: 0xf9002660  str x0, [x19,#72]
 2512: 0xd2800440  mov x0, #0x22
 2516: 0xf9002a60  str x0, [x19,#80]
 2520: 0xd2800080  mov x0, #0x4
 2524: 0xf9000a60  str x0, [x19,#16]
 2528: 0x140006ec  b .+0x1bb0
 2532: 0xf940c754  ldr x20, [x26,#392]
 2536: 0xf940cb56  ldr x22, [x26,#400]
 2540: 0xf940c355  ldr x21, [x26,#384]
 2544: 0xf940cf40  ldr x0, [x26,#408]
 2548: 0xaa0003f4  mov x20, x0
 2552: 0xaa1403e0  mov x0, x20
 2556: 0xf900cf40  str x0, [x26,#408]
 2560: 0xf900cf54  str x20, [x26,#408]
 2564: 0xf900cb56  str x22, [x26,#400]
 2568: 0xf900c355  str x21, [x26,#384]
 2572: 0xd2800060  mov x0, #0x3
 2576: 0xf9002e60  str x0, [x19,#88]
 2580: 0xd2800660  mov x0, #0x33
 2584: 0xf9003260  str x0, [x19,#96]
 2588: 0xd28000a0  mov x0, #0x5
 2592: 0xf9003e60  str x0, [x19,#120]
 2596: 0xd2800680  mov x0, #0x34
 2600: 0xf9004260  str x0, [x19,#128]
 2604: 0xd2800460  mov x0, #0x23
 2608: 0xf9004660  str x0, [x19,#136]
 2612: 0xd28000a0  mov x0, #0x5
 2616: 0xf9000a60  str x0, [x19,#16]
 2620: 0x140006d5  b .+0x1b54
 2624: 0xf940cf54  ldr x20, [x26,#408]
 2628: 0xf940cb56  ldr x22, [x26,#400]
 2632: 0xf940c355  ldr x21, [x26,#384]
 2636: 0xf940d340  ldr x0, [x26,#416]
 2640: 0xaa0003f5  mov x21, x0
 2644: 0xaa1503e0  mov x0, x21
 2648: 0xf9004340  str x0, [x26,#128]
 2652: 0xaa1603e0  mov x0, x22
 2656: 0xf9004740  str x0, [x26,#136]
 2660: 0xf900cb56  str x22, [x26,#400]
 2664: 0xf940be63  ldr x3, [x19,#376]
 2668: 0xf100c07f  cmp x3, #0x30
 2672: 0x5400094a  b.ge .+0x128
 2676: 0xf9404340  ldr x0, [x26,#128]
 2680: 0xd370fc01  lsr x1, x0, #48
 2684: 0xd29fffe2  mov x2, #0xffff
 2688: 0xeb02003f  cmp x1, x2
 2692: 0x540008a1  b.ne .+0x114
 2696: 0xd36cfc01  lsr x1, x0, #44
 2700: 0xd28001e2  mov x2, #0xf
 2704: 0x8a020021  and x1, x1, x2
 2708: 0xf100203f  cmp x1, #0x8
 2712: 0x54000801  b.ne .+0x100
 2716: 0xd340ac00  ubfx x0, x0, #0, #44
 2720: 0xf9400001  ldr x1, [x0]
 2724: 0xf9408c22  ldr x2, [x1,#280]
 2728: 0xb4000782  cbz x2, .+0xf0
 2732: 0xf9401c23  ldr x3, [x1,#56]
 2736: 0xd37df063  lsl x3, x3, #3
 2740: 0x910f0063  add x3, x3, #0x3c0
 2744: 0x8b1a0063  add x3, x3, x26
 2748: 0xf940b264  ldr x4, [x19,#352]
 2752: 0xeb04007f  cmp x3, x4
 2756: 0x540006a8  b.hi .+0xd4
 2760: 0xf9407823  ldr x3, [x1,#240]
 2764: 0x91000463  add x3, x3, #0x1
 2768: 0xf9007823  str x3, [x1,#240]
 2772: 0xf100087f  cmp x3, #0x2
 2776: 0x54000600  b.eq .+0xc0
 2780: 0xd10103ff  sub sp, sp, #0x40
 2784: 0xa9007bfd  stp x29, x30, [sp]
 2788: 0xa9016ffa  stp x26, x27, [sp,#16]
 2792: 0xf9409663  ldr x3, [x19,#296]
 2796: 0xf90013e3  str x3, [sp,#32]
 2800: 0xf9407a63  ldr x3, [x19,#240]
 2804: 0xf90017e3  str x3, [sp,#40]
 2808: 0xf9408263  ldr x3, [x19,#256]
 2812: 0xf9001be3  str x3, [sp,#48]
 2816: 0xf9404743  ldr x3, [x26,#136]
 2820: 0xf901e343  str x3, [x26,#960]
 2824: 0x910f035a  add x26, x26, #0x3c0
 2828: 0xf900027a  str x26, [x19]
 2832: 0xf9402c3b  ldr x27, [x1,#88]
 2836: 0xf900067b  str x27, [x19,#8]
 2840: 0xf9007a60  str x0, [x19,#240]
 2844: 0xd2800023  mov x3, #0x1
 2848: 0xf9009663  str x3, [x19,#296]
 2852: 0xf9409023  ldr x3, [x1,#288]
 2856: 0xf9008263  str x3, [x19,#256]
 2860: 0xf940be63  ldr x3, [x19,#376]
 2864: 0x91000463  add x3, x3, #0x1
 2868: 0xf900be63  str x3, [x19,#376]
 2872: 0xaa1303e0  mov x0, x19
 2876: 0xd63f0040  blr x2
 2880: 0xf940be63  ldr x3, [x19,#376]
 2884: 0xd1000463  sub x3, x3, #0x1
 2888: 0xf900be63  str x3, [x19,#376]
 2892: 0xa9416ffa  ldp x26, x27, [sp,#16]
 2896: 0xf94013e3  ldr x3, [sp,#32]
 2900: 0xf9009663  str x3, [x19,#296]
 2904: 0xf94017e3  ldr x3, [sp,#40]
 2908: 0xf9007a63  str x3, [x19,#240]
 2912: 0xf9401be3  ldr x3, [sp,#48]
 2916: 0xf9008263  str x3, [x19,#256]
 2920: 0xa9407bfd  ldp x29, x30, [sp]
 2924: 0x910103ff  add sp, sp, #0x40
 2928: 0xf900027a  str x26, [x19]
 2932: 0xf900067b  str x27, [x19,#8]
 2936: 0xf9400a63  ldr x3, [x19,#16]
 2940: 0xb50000e3  cbnz x3, .+0x1c
 2944: 0xf9407e60  ldr x0, [x19,#248]
 2948: 0xf9004340  str x0, [x26,#128]
 2952: 0xf940cb56  ldr x22, [x26,#400]
 2956: 0xf9404340  ldr x0, [x26,#128]
 2960: 0xaa0003f4  mov x20, x0
 2964: 0x14000014  b .+0x50
 2968: 0xf900cb56  str x22, [x26,#400]
 2972: 0xf900d355  str x21, [x26,#416]
 2976: 0xf900d754  str x20, [x26,#424]
 2980: 0xd2800200  mov x0, #0x10
 2984: 0xf9001260  str x0, [x19,#32]
 2988: 0xd2800020  mov x0, #0x1
 2992: 0xf9001660  str x0, [x19,#40]
 2996: 0xd2800020  mov x0, #0x1
 3000: 0xf9001a60  str x0, [x19,#48]
 3004: 0xd2800480  mov x0, #0x24
 3008: 0xf9001e60  str x0, [x19,#56]
 3012: 0xd2800060  mov x0, #0x3
 3016: 0xf9000a60  str x0, [x19,#16]
 3020: 0x14000671  b .+0x19c4
 3024: 0xf940d754  ldr x20, [x26,#424]
 3028: 0xf940cb56  ldr x22, [x26,#400]
 3032: 0xf940d355  ldr x21, [x26,#416]
 3036: 0xf9404340  ldr x0, [x26,#128]
 3040: 0xaa0003f4  mov x20, x0
 3044: 0xaa1603e0  mov x0, x22
 3048: 0xaa1403e1  mov x1, x20
 3052: 0xd370fc02  lsr x2, x0, #48
 3056: 0xd29fffc3  mov x3, #0xfffe
 3060: 0xeb03005f  cmp x2, x3
 3064: 0x54000161  b.ne .+0x2c
 3068: 0xd370fc22  lsr x2, x1, #48
 3072: 0xd29fffc3  mov x3, #0xfffe
 3076: 0xeb03005f  cmp x2, x3
 3080: 0x540001e1  b.ne .+0x3c
 3084: 0x9340bc00  sbfx x0, x0, #0, #48
 3088: 0x9340bc21  sbfx x1, x1, #0, #48
 3092: 0x9b017c00  mul x0, x0, x1
 3096: 0xd340bc00  ubfx x0, x0, #0, #48
 3100: 0xaa180000  orr x0, x0, x24
 3104: 0x14000010  b .+0x40
 3108: 0x9e670000  fmov d0, x0
 3112: 0xd370fc22  lsr x2, x1, #48
 3116: 0xd29fffc3  mov x3, #0xfffe
 3120: 0xeb03005f  cmp x2, x3
 3124: 0x54000101  b.ne .+0x20
 3128: 0x9340bc21  sbfx x1, x1, #0, #48
 3132: 0x9e620021  scvtf d1, x1
 3136: 0x14000006  b .+0x18
 3140: 0x9340bc00  sbfx x0, x0, #0, #48
 3144: 0x9e620000  scvtf d0, x0
 3148: 0x9e670021  fmov d1, x1
 3152: 0x14000002  b .+0x8
 3156: 0x9e670021  fmov d1, x1
 3160: 0x1e610800  fmul d0, d0, d1
 3164: 0x9e660000  fmov x0, d0
 3168: 0xaa0003f5  mov x21, x0
 3172: 0xf9400340  ldr x0, [x26]
 3176: 0xaa1503e1  mov x1, x21
 3180: 0xd370fc02  lsr x2, x0, #48
 3184: 0xd29fffc3  mov x3, #0xfffe
 3188: 0xeb03005f  cmp x2, x3
 3192: 0x54000081  b.ne .+0x10
 3196: 0x9340bc00  sbfx x0, x0, #0, #48
 3200: 0x9e620000  scvtf d0, x0
 3204: 0x14000002  b .+0x8
 3208: 0x9e670000  fmov d0, x0
 3212: 0xd370fc22  lsr x2, x1, #48
 3216: 0xd29fffc3  mov x3, #0xfffe
 3220: 0xeb03005f  cmp x2, x3
 3224: 0x54000081  b.ne .+0x10
 3228: 0x9340bc21  sbfx x1, x1, #0, #48
 3232: 0x9e620021  scvtf d1, x1
 3236: 0x14000002  b .+0x8
 3240: 0x9e670021  fmov d1, x1
 3244: 0x1e611800  fdiv d0, d0, d1
 3248: 0x9e660000  fmov x0, d0
 3252: 0xf900df40  str x0, [x26,#440]
 3256: 0xf9407b40  ldr x0, [x26,#240]
 3260: 0xf9007b40  str x0, [x26,#240]
 3264: 0xf900d754  str x20, [x26,#424]
 3268: 0xf900cb56  str x22, [x26,#400]
 3272: 0xf900db55  str x21, [x26,#432]
 3276: 0xd2800060  mov x0, #0x3
 3280: 0xf9002e60  str x0, [x19,#88]
 3284: 0xd28003c0  mov x0, #0x1e
 3288: 0xf9003260  str x0, [x19,#96]
 3292: 0xd28000c0  mov x0, #0x6
 3296: 0xf9003e60  str x0, [x19,#120]
 3300: 0xd2800700  mov x0, #0x38
 3304: 0xf9004260  str x0, [x19,#128]
 3308: 0xd2800500  mov x0, #0x28
 3312: 0xf9004660  str x0, [x19,#136]
 3316: 0xd28000a0  mov x0, #0x5
 3320: 0xf9000a60  str x0, [x19,#16]
 3324: 0x14000625  b .+0x1894
 3328: 0xf940cb56  ldr x22, [x26,#400]
 3332: 0xf940db55  ldr x21, [x26,#432]
 3336: 0xf940d754  ldr x20, [x26,#424]
 3340: 0xf940e340  ldr x0, [x26,#448]
 3344: 0xaa0003f4  mov x20, x0
 3348: 0xf9409340  ldr x0, [x26,#288]
 3352: 0xf9009340  str x0, [x26,#288]
 3356: 0xf900db55  str x21, [x26,#432]
 3360: 0xf900e354  str x20, [x26,#448]
 3364: 0xf900cb56  str x22, [x26,#400]
 3368: 0xd2800060  mov x0, #0x3
 3372: 0xf9002e60  str x0, [x19,#88]
 3376: 0xd2800480  mov x0, #0x24
 3380: 0xf9003260  str x0, [x19,#96]
 3384: 0xd28000e0  mov x0, #0x7
 3388: 0xf9003e60  str x0, [x19,#120]
 3392: 0xd2800720  mov x0, #0x39
 3396: 0xf9004260  str x0, [x19,#128]
 3400: 0xd2800520  mov x0, #0x29
 3404: 0xf9004660  str x0, [x19,#136]
 3408: 0xd28000a0  mov x0, #0x5
 3412: 0xf9000a60  str x0, [x19,#16]
 3416: 0x1400060e  b .+0x1838
 3420: 0xf940cb56  ldr x22, [x26,#400]
 3424: 0xf940db55  ldr x21, [x26,#432]
 3428: 0xf940e354  ldr x20, [x26,#448]
 3432: 0xf940e740  ldr x0, [x26,#456]
 3436: 0xaa0003f5  mov x21, x0
 3440: 0xf9409f40  ldr x0, [x26,#312]
 3444: 0xaa1503e1  mov x1, x21
 3448: 0xd370fc02  lsr x2, x0, #48
 3452: 0xd29fffc3  mov x3, #0xfffe
 3456: 0xeb03005f  cmp x2, x3
 3460: 0x54000161  b.ne .+0x2c
 3464: 0xd370fc22  lsr x2, x1, #48
 3468: 0xd29fffc3  mov x3, #0xfffe
 3472: 0xeb03005f  cmp x2, x3
 3476: 0x540001e1  b.ne .+0x3c
 3480: 0x9340bc00  sbfx x0, x0, #0, #48
 3484: 0x9340bc21  sbfx x1, x1, #0, #48
 3488: 0x9b017c00  mul x0, x0, x1
 3492: 0xd340bc00  ubfx x0, x0, #0, #48
 3496: 0xaa180000  orr x0, x0, x24
 3500: 0x14000010  b .+0x40
 3504: 0x9e670000  fmov d0, x0
 3508: 0xd370fc22  lsr x2, x1, #48
 3512: 0xd29fffc3  mov x3, #0xfffe
 3516: 0xeb03005f  cmp x2, x3
 3520: 0x54000101  b.ne .+0x20
 3524: 0x9340bc21  sbfx x1, x1, #0, #48
 3528: 0x9e620021  scvtf d1, x1
 3532: 0x14000006  b .+0x18
 3536: 0x9340bc00  sbfx x0, x0, #0, #48
 3540: 0x9e620000  scvtf d0, x0
 3544: 0x9e670021  fmov d1, x1
 3548: 0x14000002  b .+0x8
 3552: 0x9e670021  fmov d1, x1
 3556: 0x1e610800  fmul d0, d0, d1
 3560: 0x9e660000  fmov x0, d0
 3564: 0xaa0003f6  mov x22, x0
 3568: 0xaa1603e0  mov x0, x22
 3572: 0xf940df41  ldr x1, [x26,#440]
 3576: 0xd370fc02  lsr x2, x0, #48
 3580: 0xd29fffc3  mov x3, #0xfffe
 3584: 0xeb03005f  cmp x2, x3
 3588: 0x54000161  b.ne .+0x2c
 3592: 0xd370fc22  lsr x2, x1, #48
 3596: 0xd29fffc3  mov x3, #0xfffe
 3600: 0xeb03005f  cmp x2, x3
 3604: 0x540001e1  b.ne .+0x3c
 3608: 0x9340bc00  sbfx x0, x0, #0, #48
 3612: 0x9340bc21  sbfx x1, x1, #0, #48
 3616: 0x9b017c00  mul x0, x0, x1
 3620: 0xd340bc00  ubfx x0, x0, #0, #48
 3624: 0xaa180000  orr x0, x0, x24
 3628: 0x14000010  b .+0x40
 3632: 0x9e670000  fmov d0, x0
 3636: 0xd370fc22  lsr x2, x1, #48
 3640: 0xd29fffc3  mov x3, #0xfffe
 3644: 0xeb03005f  cmp x2, x3
 3648: 0x54000101  b.ne .+0x20
 3652: 0x9340bc21  sbfx x1, x1, #0, #48
 3656: 0x9e620021  scvtf d1, x1
 3660: 0x14000006  b .+0x18
 3664: 0x9340bc00  sbfx x0, x0, #0, #48
 3668: 0x9e620000  scvtf d0, x0
 3672: 0x9e670021  fmov d1, x1
 3676: 0x14000002  b .+0x8
 3680: 0x9e670021  fmov d1, x1
 3684: 0x1e610800  fmul d0, d0, d1
 3688: 0x9e660000  fmov x0, d0
 3692: 0xaa0003f5  mov x21, x0
 3696: 0xaa1403e0  mov x0, x20
 3700: 0xaa1503e1  mov x1, x21
 3704: 0xd370fc02  lsr x2, x0, #48
 3708: 0xd29fffc3  mov x3, #0xfffe
 3712: 0xeb03005f  cmp x2, x3
 3716: 0x54000161  b.ne .+0x2c
 3720: 0xd370fc22  lsr x2, x1, #48
 3724: 0xd29fffc3  mov x3, #0xfffe
 3728: 0xeb03005f  cmp x2, x3
 3732: 0x540001e1  b.ne .+0x3c
 3736: 0x9340bc00  sbfx x0, x0, #0, #48
 3740: 0x9340bc21  sbfx x1, x1, #0, #48
 3744: 0xcb010000  sub x0, x0, x1
 3748: 0xd340bc00  ubfx x0, x0, #0, #48
 3752: 0xaa180000  orr x0, x0, x24
 3756: 0x14000010  b .+0x40
 3760: 0x9e670000  fmov d0, x0
 3764: 0xd370fc22  lsr x2, x1, #48
 3768: 0xd29fffc3  mov x3, #0xfffe
 3772: 0xeb03005f  cmp x2, x3
 3776: 0x54000101  b.ne .+0x20
 3780: 0x9340bc21  sbfx x1, x1, #0, #48
 3784: 0x9e620021  scvtf d1, x1
 3788: 0x14000006  b .+0x18
 3792: 0x9340bc00  sbfx x0, x0, #0, #48
 3796: 0x9e620000  scvtf d0, x0
 3800: 0x9e670021  fmov d1, x1
 3804: 0x14000002  b .+0x8
 3808: 0x9e670021  fmov d1, x1
 3812: 0x1e613800  fsub d0, d0, d1
 3816: 0x9e660000  fmov x0, d0
 3820: 0xaa0003f6  mov x22, x0
 3824: 0xf9407b40  ldr x0, [x26,#240]
 3828: 0xf9007b40  str x0, [x26,#240]
 3832: 0xaa1603e0  mov x0, x22
 3836: 0xf901e340  str x0, [x26,#960]
 3840: 0xf900e354  str x20, [x26,#448]
 3844: 0xf900f356  str x22, [x26,#480]
 3848: 0xf900ef55  str x21, [x26,#472]
 3852: 0xd2800080  mov x0, #0x4
 3856: 0xf9002e60  str x0, [x19,#88]
 3860: 0xd28003c0  mov x0, #0x1e
 3864: 0xf9003260  str x0, [x19,#96]
 3868: 0xd28000c0  mov x0, #0x6
 3872: 0xf9003e60  str x0, [x19,#120]
 3876: 0xd2800f00  mov x0, #0x78
 3880: 0xf9003a60  str x0, [x19,#112]
 3884: 0xd28005a0  mov x0, #0x2d
 3888: 0xf9004660  str x0, [x19,#136]
 3892: 0xd28000a0  mov x0, #0x5
 3896: 0xf9000a60  str x0, [x19,#16]
 3900: 0x14000595  b .+0x1654
 3904: 0xf940e354  ldr x20, [x26,#448]
 3908: 0xf940f356  ldr x22, [x26,#480]
 3912: 0xf940ef55  ldr x21, [x26,#472]
 3916: 0xf9407b40  ldr x0, [x26,#240]
 3920: 0xf9007b40  str x0, [x26,#240]
 3924: 0xf900e354  str x20, [x26,#448]
 3928: 0xf900f356  str x22, [x26,#480]
 3932: 0xf900ef55  str x21, [x26,#472]
 3936: 0xd2800060  mov x0, #0x3
 3940: 0xf9002e60  str x0, [x19,#88]
 3944: 0xd28003c0  mov x0, #0x1e
 3948: 0xf9003260  str x0, [x19,#96]
 3952: 0xd2800100  mov x0, #0x8
 3956: 0xf9003e60  str x0, [x19,#120]
 3960: 0xd28007c0  mov x0, #0x3e
 3964: 0xf9004260  str x0, [x19,#128]
 3968: 0xd28005c0  mov x0, #0x2e
 3972: 0xf9004660  str x0, [x19,#136]
 3976: 0xd28000a0  mov x0, #0x5
 3980: 0xf9000a60  str x0, [x19,#16]
 3984: 0x14000580  b .+0x1600
 3988: 0xf940e354  ldr x20, [x26,#448]
 3992: 0xf940f356  ldr x22, [x26,#480]
 3996: 0xf940ef55  ldr x21, [x26,#472]
 4000: 0xf940fb40  ldr x0, [x26,#496]
 4004: 0xaa0003f5  mov x21, x0
 4008: 0xf9409340  ldr x0, [x26,#288]
 4012: 0xf9009340  str x0, [x26,#288]
 4016: 0xf900e354  str x20, [x26,#448]
 4020: 0xf900f356  str x22, [x26,#480]
 4024: 0xf900fb55  str x21, [x26,#496]
 4028: 0xd2800060  mov x0, #0x3
 4032: 0xf9002e60  str x0, [x19,#88]
 4036: 0xd2800480  mov x0, #0x24
 4040: 0xf9003260  str x0, [x19,#96]
 4044: 0xd28000e0  mov x0, #0x7
 4048: 0xf9003e60  str x0, [x19,#120]
 4052: 0xd28007e0  mov x0, #0x3f
 4056: 0xf9004260  str x0, [x19,#128]
 4060: 0xd28005e0  mov x0, #0x2f
 4064: 0xf9004660  str x0, [x19,#136]
 4068: 0xd28000a0  mov x0, #0x5
 4072: 0xf9000a60  str x0, [x19,#16]
 4076: 0x14000569  b .+0x15a4
 4080: 0xf940e354  ldr x20, [x26,#448]
 4084: 0xf940f356  ldr x22, [x26,#480]
 4088: 0xf940fb55  ldr x21, [x26,#496]
 4092: 0xf940ff40  ldr x0, [x26,#504]
 4096: 0xaa0003f6  mov x22, x0
 4100: 0xf940ab40  ldr x0, [x26,#336]
 4104: 0xaa1603e1  mov x1, x22
 4108: 0xd370fc02  lsr x2, x0, #48
 4112: 0xd29fffc3  mov x3, #0xfffe
 4116: 0xeb03005f  cmp x2, x3
 4120: 0x54000161  b.ne .+0x2c
 4124: 0xd370fc22  lsr x2, x1, #48
 4128: 0xd29fffc3  mov x3, #0xfffe
 4132: 0xeb03005f  cmp x2, x3
 4136: 0x540001e1  b.ne .+0x3c
 4140: 0x9340bc00  sbfx x0, x0, #0, #48
 4144: 0x9340bc21  sbfx x1, x1, #0, #48
 4148: 0x9b017c00  mul x0, x0, x1
 4152: 0xd340bc00  ubfx x0, x0, #0, #48
 4156: 0xaa180000  orr x0, x0, x24
 4160: 0x14000010  b .+0x40
 4164: 0x9e670000  fmov d0, x0
 4168: 0xd370fc22  lsr x2, x1, #48
 4172: 0xd29fffc3  mov x3, #0xfffe
 4176: 0xeb03005f  cmp x2, x3
 4180: 0x54000101  b.ne .+0x20
 4184: 0x9340bc21  sbfx x1, x1, #0, #48
 4188: 0x9e620021  scvtf d1, x1
 4192: 0x14000006  b .+0x18
 4196: 0x9340bc00  sbfx x0, x0, #0, #48
 4200: 0x9e620000  scvtf d0, x0
 4204: 0x9e670021  fmov d1, x1
 4208: 0x14000002  b .+0x8
 4212: 0x9e670021  fmov d1, x1
 4216: 0x1e610800  fmul d0, d0, d1
 4220: 0x9e660000  fmov x0, d0
 4224: 0xaa0003f7  mov x23, x0
 4228: 0xaa1703e0  mov x0, x23
 4232: 0xf940df41  ldr x1, [x26,#440]
 4236: 0xd370fc02  lsr x2, x0, #48
 4240: 0xd29fffc3  mov x3, #0xfffe
 4244: 0xeb03005f  cmp x2, x3
 4248: 0x54000161  b.ne .+0x2c
 4252: 0xd370fc22  lsr x2, x1, #48
 4256: 0xd29fffc3  mov x3, #0xfffe
 4260: 0xeb03005f  cmp x2, x3
 4264: 0x540001e1  b.ne .+0x3c
 4268: 0x9340bc00  sbfx x0, x0, #0, #48
 4272: 0x9340bc21  sbfx x1, x1, #0, #48
 4276: 0x9b017c00  mul x0, x0, x1
 4280: 0xd340bc00  ubfx x0, x0, #0, #48
 4284: 0xaa180000  orr x0, x0, x24
 4288: 0x14000010  b .+0x40
 4292: 0x9e670000  fmov d0, x0
 4296: 0xd370fc22  lsr x2, x1, #48
 4300: 0xd29fffc3  mov x3, #0xfffe
 4304: 0xeb03005f  cmp x2, x3
 4308: 0x54000101  b.ne .+0x20
 4312: 0x9340bc21  sbfx x1, x1, #0, #48
 4316: 0x9e620021  scvtf d1, x1
 4320: 0x14000006  b .+0x18
 4324: 0x9340bc00  sbfx x0, x0, #0, #48
 4328: 0x9e620000  scvtf d0, x0
 4332: 0x9e670021  fmov d1, x1
 4336: 0x14000002  b .+0x8
 4340: 0x9e670021  fmov d1, x1
 4344: 0x1e610800  fmul d0, d0, d1
 4348: 0x9e660000  fmov x0, d0
 4352: 0xaa0003f6  mov x22, x0
 4356: 0xaa1503e0  mov x0, x21
 4360: 0xaa1603e1  mov x1, x22
 4364: 0xd370fc02  lsr x2, x0, #48
 4368: 0xd29fffc3  mov x3, #0xfffe
 4372: 0xeb03005f  cmp x2, x3
 4376: 0x54000161  b.ne .+0x2c
 4380: 0xd370fc22  lsr x2, x1, #48
 4384: 0xd29fffc3  mov x3, #0xfffe
 4388: 0xeb03005f  cmp x2, x3
 4392: 0x540001e1  b.ne .+0x3c
 4396: 0x9340bc00  sbfx x0, x0, #0, #48
 4400: 0x9340bc21  sbfx x1, x1, #0, #48
 4404: 0xcb010000  sub x0, x0, x1
 4408: 0xd340bc00  ubfx x0, x0, #0, #48
 4412: 0xaa180000  orr x0, x0, x24
 4416: 0x14000010  b .+0x40
 4420: 0x9e670000  fmov d0, x0
 4424: 0xd370fc22  lsr x2, x1, #48
 4428: 0xd29fffc3  mov x3, #0xfffe
 4432: 0xeb03005f  cmp x2, x3
 4436: 0x54000101  b.ne .+0x20
 4440: 0x9340bc21  sbfx x1, x1, #0, #48
 4444: 0x9e620021  scvtf d1, x1
 4448: 0x14000006  b .+0x18
 4452: 0x9340bc00  sbfx x0, x0, #0, #48
 4456: 0x9e620000  scvtf d0, x0
 4460: 0x9e670021  fmov d1, x1
 4464: 0x14000002  b .+0x8
 4468: 0x9e670021  fmov d1, x1
 4472: 0x1e613800  fsub d0, d0, d1
 4476: 0x9e660000  fmov x0, d0
 4480: 0xaa0003f7  mov x23, x0
 4484: 0xf9407b40  ldr x0, [x26,#240]
 4488: 0xf9007b40  str x0, [x26,#240]
 4492: 0xaa1703e0  mov x0, x23
 4496: 0xf901e740  str x0, [x26,#968]
 4500: 0xf9010756  str x22, [x26,#520]
 4504: 0xf900fb55  str x21, [x26,#496]
 4508: 0xf9010b57  str x23, [x26,#528]
 4512: 0xf900e354  str x20, [x26,#448]
 4516: 0xd2800080  mov x0, #0x4
 4520: 0xf9002e60  str x0, [x19,#88]
 4524: 0xd28003c0  mov x0, #0x1e
 4528: 0xf9003260  str x0, [x19,#96]
 4532: 0xd2800100  mov x0, #0x8
 4536: 0xf9003e60  str x0, [x19,#120]
 4540: 0xd2800f20  mov x0, #0x79
 4544: 0xf9003a60  str x0, [x19,#112]
 4548: 0xd2800660  mov x0, #0x33
 4552: 0xf9004660  str x0, [x19,#136]
 4556: 0xd28000a0  mov x0, #0x5
 4560: 0xf9000a60  str x0, [x19,#16]
 4564: 0x140004ef  b .+0x13bc
 4568: 0xf940e354  ldr x20, [x26,#448]
 4572: 0xf9410756  ldr x22, [x26,#520]
 4576: 0xf940fb55  ldr x21, [x26,#496]
 4580: 0xf9410b57  ldr x23, [x26,#528]
 4584: 0xf9407b40  ldr x0, [x26,#240]
 4588: 0xf9007b40  str x0, [x26,#240]
 4592: 0xf900e354  str x20, [x26,#448]
 4596: 0xf9010756  str x22, [x26,#520]
 4600: 0xf900fb55  str x21, [x26,#496]
 4604: 0xf9010b57  str x23, [x26,#528]
 4608: 0xd2800060  mov x0, #0x3
 4612: 0xf9002e60  str x0, [x19,#88]
 4616: 0xd28003c0  mov x0, #0x1e
 4620: 0xf9003260  str x0, [x19,#96]
 4624: 0xd2800120  mov x0, #0x9
 4628: 0xf9003e60  str x0, [x19,#120]
 4632: 0xd2800880  mov x0, #0x44
 4636: 0xf9004260  str x0, [x19,#128]
 4640: 0xd2800680  mov x0, #0x34
 4644: 0xf9004660  str x0, [x19,#136]
 4648: 0xd28000a0  mov x0, #0x5
 4652: 0xf9000a60  str x0, [x19,#16]
 4656: 0x140004d8  b .+0x1360
 4660: 0xf940e354  ldr x20, [x26,#448]
 4664: 0xf9410756  ldr x22, [x26,#520]
 4668: 0xf940fb55  ldr x21, [x26,#496]
 4672: 0xf9410b57  ldr x23, [x26,#528]
 4676: 0xf9411340  ldr x0, [x26,#544]
 4680: 0xaa0003f6  mov x22, x0
 4684: 0xf9409340  ldr x0, [x26,#288]
 4688: 0xf9009340  str x0, [x26,#288]
 4692: 0xf9010b57  str x23, [x26,#528]
 4696: 0xf900e354  str x20, [x26,#448]
 4700: 0xf9011356  str x22, [x26,#544]
 4704: 0xf900fb55  str x21, [x26,#496]
 4708: 0xd2800060  mov x0, #0x3
 4712: 0xf9002e60  str x0, [x19,#88]
 4716: 0xd2800480  mov x0, #0x24
 4720: 0xf9003260  str x0, [x19,#96]
 4724: 0xd28000e0  mov x0, #0x7
 4728: 0xf9003e60  str x0, [x19,#120]
 4732: 0xd28008a0  mov x0, #0x45
 4736: 0xf9004260  str x0, [x19,#128]
 4740: 0xd28006a0  mov x0, #0x35
 4744: 0xf9004660  str x0, [x19,#136]
 4748: 0xd28000a0  mov x0, #0x5
 4752: 0xf9000a60  str x0, [x19,#16]
 4756: 0x140004bf  b .+0x12fc
 4760: 0xf9410b57  ldr x23, [x26,#528]
 4764: 0xf940e354  ldr x20, [x26,#448]
 4768: 0xf9411356  ldr x22, [x26,#544]
 4772: 0xf940fb55  ldr x21, [x26,#496]
 4776: 0xf9411740  ldr x0, [x26,#552]
 4780: 0xaa0003f7  mov x23, x0
 4784: 0xf940b740  ldr x0, [x26,#360]
 4788: 0xaa1703e1  mov x1, x23
 4792: 0xd370fc02  lsr x2, x0, #48
 4796: 0xd29fffc3  mov x3, #0xfffe
 4800: 0xeb03005f  cmp x2, x3
 4804: 0x54000161  b.ne .+0x2c
 4808: 0xd370fc22  lsr x2, x1, #48
 4812: 0xd29fffc3  mov x3, #0xfffe
 4816: 0xeb03005f  cmp x2, x3
 4820: 0x540001e1  b.ne .+0x3c
 4824: 0x9340bc00  sbfx x0, x0, #0, #48
 4828: 0x9340bc21  sbfx x1, x1, #0, #48
 4832: 0x9b017c00  mul x0, x0, x1
 4836: 0xd340bc00  ubfx x0, x0, #0, #48
 4840: 0xaa180000  orr x0, x0, x24
 4844: 0x14000010  b .+0x40
 4848: 0x9e670000  fmov d0, x0
 4852: 0xd370fc22  lsr x2, x1, #48
 4856: 0xd29fffc3  mov x3, #0xfffe
 4860: 0xeb03005f  cmp x2, x3
 4864: 0x54000101  b.ne .+0x20
 4868: 0x9340bc21  sbfx x1, x1, #0, #48
 4872: 0x9e620021  scvtf d1, x1
 4876: 0x14000006  b .+0x18
 4880: 0x9340bc00  sbfx x0, x0, #0, #48
 4884: 0x9e620000  scvtf d0, x0
 4888: 0x9e670021  fmov d1, x1
 4892: 0x14000002  b .+0x8
 4896: 0x9e670021  fmov d1, x1
 4900: 0x1e610800  fmul d0, d0, d1
 4904: 0x9e660000  fmov x0, d0
 4908: 0xaa0003f4  mov x20, x0
 4912: 0xaa1403e0  mov x0, x20
 4916: 0xf940df41  ldr x1, [x26,#440]
 4920: 0xd370fc02  lsr x2, x0, #48
 4924: 0xd29fffc3  mov x3, #0xfffe
 4928: 0xeb03005f  cmp x2, x3
 4932: 0x54000161  b.ne .+0x2c
 4936: 0xd370fc22  lsr x2, x1, #48
 4940: 0xd29fffc3  mov x3, #0xfffe
 4944: 0xeb03005f  cmp x2, x3
 4948: 0x540001e1  b.ne .+0x3c
 4952: 0x9340bc00  sbfx x0, x0, #0, #48
 4956: 0x9340bc21  sbfx x1, x1, #0, #48
 4960: 0x9b017c00  mul x0, x0, x1
 4964: 0xd340bc00  ubfx x0, x0, #0, #48
 4968: 0xaa180000  orr x0, x0, x24
 4972: 0x14000010  b .+0x40
 4976: 0x9e670000  fmov d0, x0
 4980: 0xd370fc22  lsr x2, x1, #48
 4984: 0xd29fffc3  mov x3, #0xfffe
 4988: 0xeb03005f  cmp x2, x3
 4992: 0x54000101  b.ne .+0x20
 4996: 0x9340bc21  sbfx x1, x1, #0, #48
 5000: 0x9e620021  scvtf d1, x1
 5004: 0x14000006  b .+0x18
 5008: 0x9340bc00  sbfx x0, x0, #0, #48
 5012: 0x9e620000  scvtf d0, x0
 5016: 0x9e670021  fmov d1, x1
 5020: 0x14000002  b .+0x8
 5024: 0x9e670021  fmov d1, x1
 5028: 0x1e610800  fmul d0, d0, d1
 5032: 0x9e660000  fmov x0, d0
 5036: 0xaa0003f7  mov x23, x0
 5040: 0xaa1603e0  mov x0, x22
 5044: 0xaa1703e1  mov x1, x23
 5048: 0xd370fc02  lsr x2, x0, #48
 5052: 0xd29fffc3  mov x3, #0xfffe
 5056: 0xeb03005f  cmp x2, x3
 5060: 0x54000161  b.ne .+0x2c
 5064: 0xd370fc22  lsr x2, x1, #48
 5068: 0xd29fffc3  mov x3, #0xfffe
 5072: 0xeb03005f  cmp x2, x3
 5076: 0x540001e1  b.ne .+0x3c
 5080: 0x9340bc00  sbfx x0, x0, #0, #48
 5084: 0x9340bc21  sbfx x1, x1, #0, #48
 5088: 0xcb010000  sub x0, x0, x1
 5092: 0xd340bc00  ubfx x0, x0, #0, #48
 5096: 0xaa180000  orr x0, x0, x24
 5100: 0x14000010  b .+0x40
 5104: 0x9e670000  fmov d0, x0
 5108: 0xd370fc22  lsr x2, x1, #48
 5112: 0xd29fffc3  mov x3, #0xfffe
 5116: 0xeb03005f  cmp x2, x3
 5120: 0x54000101  b.ne .+0x20
 5124: 0x9340bc21  sbfx x1, x1, #0, #48
 5128: 0x9e620021  scvtf d1, x1
 5132: 0x14000006  b .+0x18
 5136: 0x9340bc00  sbfx x0, x0, #0, #48
 5140: 0x9e620000  scvtf d0, x0
 5144: 0x9e670021  fmov d1, x1
 5148: 0x14000002  b .+0x8
 5152: 0x9e670021  fmov d1, x1
 5156: 0x1e613800  fsub d0, d0, d1
 5160: 0x9e660000  fmov x0, d0
 5164: 0xaa0003f4  mov x20, x0
 5168: 0xf9407b40  ldr x0, [x26,#240]
 5172: 0xf9007b40  str x0, [x26,#240]
 5176: 0xaa1403e0  mov x0, x20
 5180: 0xf901eb40  str x0, [x26,#976]
 5184: 0xf9012354  str x20, [x26,#576]
 5188: 0xf9011356  str x22, [x26,#544]
 5192: 0xf900fb55  str x21, [x26,#496]
 5196: 0xf9011f57  str x23, [x26,#568]
 5200: 0xd2800080  mov x0, #0x4
 5204: 0xf9002e60  str x0, [x19,#88]
 5208: 0xd28003c0  mov x0, #0x1e
 5212: 0xf9003260  str x0, [x19,#96]
 5216: 0xd2800120  mov x0, #0x9
 5220: 0xf9003e60  str x0, [x19,#120]
 5224: 0xd2800f40  mov x0, #0x7a
 5228: 0xf9003a60  str x0, [x19,#112]
 5232: 0xd2800720  mov x0, #0x39
 5236: 0xf9004660  str x0, [x19,#136]
 5240: 0xd28000a0  mov x0, #0x5
 5244: 0xf9000a60  str x0, [x19,#16]
 5248: 0x14000444  b .+0x1110
 5252: 0xf9412354  ldr x20, [x26,#576]
 5256: 0xf9411356  ldr x22, [x26,#544]
 5260: 0xf940fb55  ldr x21, [x26,#496]
 5264: 0xf9411f57  ldr x23, [x26,#568]
 5268: 0xf9409340  ldr x0, [x26,#288]
 5272: 0xf9009340  str x0, [x26,#288]
 5276: 0xf9011356  str x22, [x26,#544]
 5280: 0xf900fb55  str x21, [x26,#496]
 5284: 0xf9011f57  str x23, [x26,#568]
 5288: 0xf9012354  str x20, [x26,#576]
 5292: 0xd2800060  mov x0, #0x3
 5296: 0xf9002e60  str x0, [x19,#88]
 5300: 0xd2800480  mov x0, #0x24
 5304: 0xf9003260  str x0, [x19,#96]
 5308: 0xd28000c0  mov x0, #0x6
 5312: 0xf9003e60  str x0, [x19,#120]
 5316: 0xd2800940  mov x0, #0x4a
 5320: 0xf9004260  str x0, [x19,#128]
 5324: 0xd2800740  mov x0, #0x3a
 5328: 0xf9004660  str x0, [x19,#136]
 5332: 0xd28000a0  mov x0, #0x5
 5336: 0xf9000a60  str x0, [x19,#16]
 5340: 0x1400042d  b .+0x10b4
 5344: 0xf9412354  ldr x20, [x26,#576]
 5348: 0xf9411356  ldr x22, [x26,#544]
 5352: 0xf940fb55  ldr x21, [x26,#496]
 5356: 0xf9411f57  ldr x23, [x26,#568]
 5360: 0xf9412b40  ldr x0, [x26,#592]
 5364: 0xaa0003f4  mov x20, x0
 5368: 0xf9407b40  ldr x0, [x26,#240]
 5372: 0xf9007b40  str x0, [x26,#240]
 5376: 0xf9012b54  str x20, [x26,#592]
 5380: 0xf9011356  str x22, [x26,#544]
 5384: 0xf900fb55  str x21, [x26,#496]
 5388: 0xf9011f57  str x23, [x26,#568]
 5392: 0xd2800060  mov x0, #0x3
 5396: 0xf9002e60  str x0, [x19,#88]
 5400: 0xd28003c0  mov x0, #0x1e
 5404: 0xf9003260  str x0, [x19,#96]
 5408: 0xd28000e0  mov x0, #0x7
 5412: 0xf9003e60  str x0, [x19,#120]
 5416: 0xd2800960  mov x0, #0x4b
 5420: 0xf9004260  str x0, [x19,#128]
 5424: 0xd2800760  mov x0, #0x3b
 5428: 0xf9004660  str x0, [x19,#136]
 5432: 0xd28000a0  mov x0, #0x5
 5436: 0xf9000a60  str x0, [x19,#16]
 5440: 0x14000414  b .+0x1050
 5444: 0xf9411f57  ldr x23, [x26,#568]
 5448: 0xf9412b54  ldr x20, [x26,#592]
 5452: 0xf9411356  ldr x22, [x26,#544]
 5456: 0xf940fb55  ldr x21, [x26,#496]
 5460: 0xf9412f40  ldr x0, [x26,#600]
 5464: 0xaa0003f7  mov x23, x0
 5468: 0xf9409f40  ldr x0, [x26,#312]
 5472: 0xaa1703e1  mov x1, x23
 5476: 0xd370fc02  lsr x2, x0, #48
 5480: 0xd29fffc3  mov x3, #0xfffe
 5484: 0xeb03005f  cmp x2, x3
 5488: 0x54000161  b.ne .+0x2c
 5492: 0xd370fc22  lsr x2, x1, #48
 5496: 0xd29fffc3  mov x3, #0xfffe
 5500: 0xeb03005f  cmp x2, x3
 5504: 0x540001e1  b.ne .+0x3c
 5508: 0x9340bc00  sbfx x0, x0, #0, #48
 5512: 0x9340bc21  sbfx x1, x1, #0, #48
 5516: 0x9b017c00  mul x0, x0, x1
 5520: 0xd340bc00  ubfx x0, x0, #0, #48
 5524: 0xaa180000  orr x0, x0, x24
 5528: 0x14000010  b .+0x40
 5532: 0x9e670000  fmov d0, x0
 5536: 0xd370fc22  lsr x2, x1, #48
 5540: 0xd29fffc3  mov x3, #0xfffe
 5544: 0xeb03005f  cmp x2, x3
 5548: 0x54000101  b.ne .+0x20
 5552: 0x9340bc21  sbfx x1, x1, #0, #48
 5556: 0x9e620021  scvtf d1, x1
 5560: 0x14000006  b .+0x18
 5564: 0x9340bc00  sbfx x0, x0, #0, #48
 5568: 0x9e620000  scvtf d0, x0
 5572: 0x9e670021  fmov d1, x1
 5576: 0x14000002  b .+0x8
 5580: 0x9e670021  fmov d1, x1
 5584: 0x1e610800  fmul d0, d0, d1
 5588: 0x9e660000  fmov x0, d0
 5592: 0xaa0003fc  mov x28, x0
 5596: 0xaa1c03e0  mov x0, x28
 5600: 0xf940df41  ldr x1, [x26,#440]
 5604: 0xd370fc02  lsr x2, x0, #48
 5608: 0xd29fffc3  mov x3, #0xfffe
 5612: 0xeb03005f  cmp x2, x3
 5616: 0x54000161  b.ne .+0x2c
 5620: 0xd370fc22  lsr x2, x1, #48
 5624: 0xd29fffc3  mov x3, #0xfffe
 5628: 0xeb03005f  cmp x2, x3
 5632: 0x540001e1  b.ne .+0x3c
 5636: 0x9340bc00  sbfx x0, x0, #0, #48
 5640: 0x9340bc21  sbfx x1, x1, #0, #48
 5644: 0x9b017c00  mul x0, x0, x1
 5648: 0xd340bc00  ubfx x0, x0, #0, #48
 5652: 0xaa180000  orr x0, x0, x24
 5656: 0x14000010  b .+0x40
 5660: 0x9e670000  fmov d0, x0
 5664: 0xd370fc22  lsr x2, x1, #48
 5668: 0xd29fffc3  mov x3, #0xfffe
 5672: 0xeb03005f  cmp x2, x3
 5676: 0x54000101  b.ne .+0x20
 5680: 0x9340bc21  sbfx x1, x1, #0, #48
 5684: 0x9e620021  scvtf d1, x1
 5688: 0x14000006  b .+0x18
 5692: 0x9340bc00  sbfx x0, x0, #0, #48
 5696: 0x9e620000  scvtf d0, x0
 5700: 0x9e670021  fmov d1, x1
 5704: 0x14000002  b .+0x8
 5708: 0x9e670021  fmov d1, x1
 5712: 0x1e610800  fmul d0, d0, d1
 5716: 0x9e660000  fmov x0, d0
 5720: 0xaa0003f7  mov x23, x0
 5724: 0xaa1403e0  mov x0, x20
 5728: 0xaa1703e1  mov x1, x23
 5732: 0xd370fc02  lsr x2, x0, #48
 5736: 0xd29fffc3  mov x3, #0xfffe
 5740: 0xeb03005f  cmp x2, x3
 5744: 0x54000161  b.ne .+0x2c
 5748: 0xd370fc22  lsr x2, x1, #48
 5752: 0xd29fffc3  mov x3, #0xfffe
 5756: 0xeb03005f  cmp x2, x3
 5760: 0x540001e1  b.ne .+0x3c
 5764: 0x9340bc00  sbfx x0, x0, #0, #48
 5768: 0x9340bc21  sbfx x1, x1, #0, #48
 5772: 0x8b010000  add x0, x0, x1
 5776: 0xd340bc00  ubfx x0, x0, #0, #48
 5780: 0xaa180000  orr x0, x0, x24
 5784: 0x14000010  b .+0x40
 5788: 0x9e670000  fmov d0, x0
 5792: 0xd370fc22  lsr x2, x1, #48
 5796: 0xd29fffc3  mov x3, #0xfffe
 5800: 0xeb03005f  cmp x2, x3
 5804: 0x54000101  b.ne .+0x20
 5808: 0x9340bc21  sbfx x1, x1, #0, #48
 5812: 0x9e620021  scvtf d1, x1
 5816: 0x14000006  b .+0x18
 5820: 0x9340bc00  sbfx x0, x0, #0, #48
 5824: 0x9e620000  scvtf d0, x0
 5828: 0x9e670021  fmov d1, x1
 5832: 0x14000002  b .+0x8
 5836: 0x9e670021  fmov d1, x1
 5840: 0x1e612800  fadd d0, d0, d1
 5844: 0x9e660000  fmov x0, d0
 5848: 0xaa0003fc  mov x28, x0
 5852: 0xf9409340  ldr x0, [x26,#288]
 5856: 0xf9009340  str x0, [x26,#288]
 5860: 0xaa1c03e0  mov x0, x28
 5864: 0xf901ef40  str x0, [x26,#984]
 5868: 0xf9012b54  str x20, [x26,#592]
 5872: 0xf9011356  str x22, [x26,#544]
 5876: 0xf900fb55  str x21, [x26,#496]
 5880: 0xf9013757  str x23, [x26,#616]
 5884: 0xf9013b5c  str x28, [x26,#624]
 5888: 0xd2800080  mov x0, #0x4
 5892: 0xf9002e60  str x0, [x19,#88]
 5896: 0xd2800480  mov x0, #0x24
 5900: 0xf9003260  str x0, [x19,#96]
 5904: 0xd28000c0  mov x0, #0x6
 5908: 0xf9003e60  str x0, [x19,#120]
 5912: 0xd2800f60  mov x0, #0x7b
 5916: 0xf9003a60  str x0, [x19,#112]
 5920: 0xd28007e0  mov x0, #0x3f
 5924: 0xf9004660  str x0, [x19,#136]
 5928: 0xd28000a0  mov x0, #0x5
 5932: 0xf9000a60  str x0, [x19,#16]
 5936: 0x14000398  b .+0xe60
 5940: 0xf9412b54  ldr x20, [x26,#592]
 5944: 0xf9411356  ldr x22, [x26,#544]
 5948: 0xf940fb55  ldr x21, [x26,#496]
 5952: 0xf9413757  ldr x23, [x26,#616]
 5956: 0xf9413b5c  ldr x28, [x26,#624]
 5960: 0xf9409340  ldr x0, [x26,#288]
 5964: 0xf9009340  str x0, [x26,#288]
 5968: 0xf9012b54  str x20, [x26,#592]
 5972: 0xf9011356  str x22, [x26,#544]
 5976: 0xf900fb55  str x21, [x26,#496]
 5980: 0xf9013757  str x23, [x26,#616]
 5984: 0xf9013b5c  str x28, [x26,#624]
 5988: 0xd2800060  mov x0, #0x3
 5992: 0xf9002e60  str x0, [x19,#88]
 5996: 0xd2800480  mov x0, #0x24
 6000: 0xf9003260  str x0, [x19,#96]
 6004: 0xd2800100  mov x0, #0x8
 6008: 0xf9003e60  str x0, [x19,#120]
 6012: 0xd2800a00  mov x0, #0x50
 6016: 0xf9004260  str x0, [x19,#128]
 6020: 0xd2800800  mov x0, #0x40
 6024: 0xf9004660  str x0, [x19,#136]
 6028: 0xd28000a0  mov x0, #0x5
 6032: 0xf9000a60  str x0, [x19,#16]
 6036: 0x1400037f  b .+0xdfc
 6040: 0xf9411356  ldr x22, [x26,#544]
 6044: 0xf940fb55  ldr x21, [x26,#496]
 6048: 0xf9413757  ldr x23, [x26,#616]
 6052: 0xf9413b5c  ldr x28, [x26,#624]
 6056: 0xf9412b54  ldr x20, [x26,#592]
 6060: 0xf9414340  ldr x0, [x26,#640]
 6064: 0xaa0003f7  mov x23, x0
 6068: 0xf9407b40  ldr x0, [x26,#240]
 6072: 0xf9007b40  str x0, [x26,#240]
 6076: 0xf9012b54  str x20, [x26,#592]
 6080: 0xf9011356  str x22, [x26,#544]
 6084: 0xf900fb55  str x21, [x26,#496]
 6088: 0xf9014357  str x23, [x26,#640]
 6092: 0xf9013b5c  str x28, [x26,#624]
 6096: 0xd2800060  mov x0, #0x3
 6100: 0xf9002e60  str x0, [x19,#88]
 6104: 0xd28003c0  mov x0, #0x1e
 6108: 0xf9003260  str x0, [x19,#96]
 6112: 0xd28000e0  mov x0, #0x7
 6116: 0xf9003e60  str x0, [x19,#120]
 6120: 0xd2800a20  mov x0, #0x51
 6124: 0xf9004260  str x0, [x19,#128]
 6128: 0xd2800820  mov x0, #0x41
 6132: 0xf9004660  str x0, [x19,#136]
 6136: 0xd28000a0  mov x0, #0x5
 6140: 0xf9000a60  str x0, [x19,#16]
 6144: 0x14000364  b .+0xd90
 6148: 0xf9412b54  ldr x20, [x26,#592]
 6152: 0xf9411356  ldr x22, [x26,#544]
 6156: 0xf940fb55  ldr x21, [x26,#496]
 6160: 0xf9414357  ldr x23, [x26,#640]
 6164: 0xf9413b5c  ldr x28, [x26,#624]
 6168: 0xf9414740  ldr x0, [x26,#648]
 6172: 0xaa0003fc  mov x28, x0
 6176: 0xf940ab40  ldr x0, [x26,#336]
 6180: 0xaa1c03e1  mov x1, x28
 6184: 0xd370fc02  lsr x2, x0, #48
 6188: 0xd29fffc3  mov x3, #0xfffe
 6192: 0xeb03005f  cmp x2, x3
 6196: 0x54000161  b.ne .+0x2c
 6200: 0xd370fc22  lsr x2, x1, #48
 6204: 0xd29fffc3  mov x3, #0xfffe
 6208: 0xeb03005f  cmp x2, x3
 6212: 0x540001e1  b.ne .+0x3c
 6216: 0x9340bc00  sbfx x0, x0, #0, #48
 6220: 0x9340bc21  sbfx x1, x1, #0, #48
 6224: 0x9b017c00  mul x0, x0, x1
 6228: 0xd340bc00  ubfx x0, x0, #0, #48
 6232: 0xaa180000  orr x0, x0, x24
 6236: 0x14000010  b .+0x40
 6240: 0x9e670000  fmov d0, x0
 6244: 0xd370fc22  lsr x2, x1, #48
 6248: 0xd29fffc3  mov x3, #0xfffe
 6252: 0xeb03005f  cmp x2, x3
 6256: 0x54000101  b.ne .+0x20
 6260: 0x9340bc21  sbfx x1, x1, #0, #48
 6264: 0x9e620021  scvtf d1, x1
 6268: 0x14000006  b .+0x18
 6272: 0x9340bc00  sbfx x0, x0, #0, #48
 6276: 0x9e620000  scvtf d0, x0
 6280: 0x9e670021  fmov d1, x1
 6284: 0x14000002  b .+0x8
 6288: 0x9e670021  fmov d1, x1
 6292: 0x1e610800  fmul d0, d0, d1
 6296: 0x9e660000  fmov x0, d0
 6300: 0xaa0003f5  mov x21, x0
 6304: 0xaa1503e0  mov x0, x21
 6308: 0xf940df41  ldr x1, [x26,#440]
 6312: 0xd370fc02  lsr x2, x0, #48
 6316: 0xd29fffc3  mov x3, #0xfffe
 6320: 0xeb03005f  cmp x2, x3
 6324: 0x54000161  b.ne .+0x2c
 6328: 0xd370fc22  lsr x2, x1, #48
 6332: 0xd29fffc3  mov x3, #0xfffe
 6336: 0xeb03005f  cmp x2, x3
 6340: 0x540001e1  b.ne .+0x3c
 6344: 0x9340bc00  sbfx x0, x0, #0, #48
 6348: 0x9340bc21  sbfx x1, x1, #0, #48
 6352: 0x9b017c00  mul x0, x0, x1
 6356: 0xd340bc00  ubfx x0, x0, #0, #48
 6360: 0xaa180000  orr x0, x0, x24
 6364: 0x14000010  b .+0x40
 6368: 0x9e670000  fmov d0, x0
 6372: 0xd370fc22  lsr x2, x1, #48
 6376: 0xd29fffc3  mov x3, #0xfffe
 6380: 0xeb03005f  cmp x2, x3
 6384: 0x54000101  b.ne .+0x20
 6388: 0x9340bc21  sbfx x1, x1, #0, #48
 6392: 0x9e620021  scvtf d1, x1
 6396: 0x14000006  b .+0x18
 6400: 0x9340bc00  sbfx x0, x0, #0, #48
 6404: 0x9e620000  scvtf d0, x0
 6408: 0x9e670021  fmov d1, x1
 6412: 0x14000002  b .+0x8
 6416: 0x9e670021  fmov d1, x1
 6420: 0x1e610800  fmul d0, d0, d1
 6424: 0x9e660000  fmov x0, d0
 6428: 0xaa0003fc  mov x28, x0
 6432: 0xaa1703e0  mov x0, x23
 6436: 0xaa1c03e1  mov x1, x28
 6440: 0xd370fc02  lsr x2, x0, #48
 6444: 0xd29fffc3  mov x3, #0xfffe
 6448: 0xeb03005f  cmp x2, x3
 6452: 0x54000161  b.ne .+0x2c
 6456: 0xd370fc22  lsr x2, x1, #48
 6460: 0xd29fffc3  mov x3, #0xfffe
 6464: 0xeb03005f  cmp x2, x3
 6468: 0x540001e1  b.ne .+0x3c
 6472: 0x9340bc00  sbfx x0, x0, #0, #48
 6476: 0x9340bc21  sbfx x1, x1, #0, #48
 6480: 0x8b010000  add x0, x0, x1
 6484: 0xd340bc00  ubfx x0, x0, #0, #48
 6488: 0xaa180000  orr x0, x0, x24
 6492: 0x14000010  b .+0x40
 6496: 0x9e670000  fmov d0, x0
 6500: 0xd370fc22  lsr x2, x1, #48
 6504: 0xd29fffc3  mov x3, #0xfffe
 6508: 0xeb03005f  cmp x2, x3
 6512: 0x54000101  b.ne .+0x20
 6516: 0x9340bc21  sbfx x1, x1, #0, #48
 6520: 0x9e620021  scvtf d1, x1
 6524: 0x14000006  b .+0x18
 6528: 0x9340bc00  sbfx x0, x0, #0, #48
 6532: 0x9e620000  scvtf d0, x0
 6536: 0x9e670021  fmov d1, x1
 6540: 0x14000002  b .+0x8
 6544: 0x9e670021  fmov d1, x1
 6548: 0x1e612800  fadd d0, d0, d1
 6552: 0x9e660000  fmov x0, d0
 6556: 0xaa0003f5  mov x21, x0
 6560: 0xf9409340  ldr x0, [x26,#288]
 6564: 0xf9009340  str x0, [x26,#288]
 6568: 0xaa1503e0  mov x0, x21
 6572: 0xf901f340  str x0, [x26,#992]
 6576: 0xf9015355  str x21, [x26,#672]
 6580: 0xf9014357  str x23, [x26,#640]
 6584: 0xf9014f5c  str x28, [x26,#664]
 6588: 0xf9012b54  str x20, [x26,#592]
 6592: 0xf9011356  str x22, [x26,#544]
 6596: 0xd2800080  mov x0, #0x4
 6600: 0xf9002e60  str x0, [x19,#88]
 6604: 0xd2800480  mov x0, #0x24
 6608: 0xf9003260  str x0, [x19,#96]
 6612: 0xd2800100  mov x0, #0x8
 6616: 0xf9003e60  str x0, [x19,#120]
 6620: 0xd2800f80  mov x0, #0x7c
 6624: 0xf9003a60  str x0, [x19,#112]
 6628: 0xd28008a0  mov x0, #0x45
 6632: 0xf9004660  str x0, [x19,#136]
 6636: 0xd28000a0  mov x0, #0x5
 6640: 0xf9000a60  str x0, [x19,#16]
 6644: 0x140002e7  b .+0xb9c
 6648: 0xf9412b54  ldr x20, [x26,#592]
 6652: 0xf9411356  ldr x22, [x26,#544]
 6656: 0xf9415355  ldr x21, [x26,#672]
 6660: 0xf9414357  ldr x23, [x26,#640]
 6664: 0xf9414f5c  ldr x28, [x26,#664]
 6668: 0xf9409340  ldr x0, [x26,#288]
 6672: 0xf9009340  str x0, [x26,#288]
 6676: 0xf9015355  str x21, [x26,#672]
 6680: 0xf9014357  str x23, [x26,#640]
 6684: 0xf9014f5c  str x28, [x26,#664]
 6688: 0xf9012b54  str x20, [x26,#592]
 6692: 0xf9011356  str x22, [x26,#544]
 6696: 0xd2800060  mov x0, #0x3
 6700: 0xf9002e60  str x0, [x19,#88]
 6704: 0xd2800480  mov x0, #0x24
 6708: 0xf9003260  str x0, [x19,#96]
 6712: 0xd2800120  mov x0, #0x9
 6716: 0xf9003e60  str x0, [x19,#120]
 6720: 0xd2800ac0  mov x0, #0x56
 6724: 0xf9004260  str x0, [x19,#128]
 6728: 0xd28008c0  mov x0, #0x46
 6732: 0xf9004660  str x0, [x19,#136]
 6736: 0xd28000a0  mov x0, #0x5
 6740: 0xf9000a60  str x0, [x19,#16]
 6744: 0x140002ce  b .+0xb38
 6748: 0xf9415355  ldr x21, [x26,#672]
 6752: 0xf9414357  ldr x23, [x26,#640]
 6756: 0xf9414f5c  ldr x28, [x26,#664]
 6760: 0xf9412b54  ldr x20, [x26,#592]
 6764: 0xf9411356  ldr x22, [x26,#544]
 6768: 0xf9415b40  ldr x0, [x26,#688]
 6772: 0xaa0003f5  mov x21, x0
 6776: 0xf9407b40  ldr x0, [x26,#240]
 6780: 0xf9007b40  str x0, [x26,#240]
 6784: 0xf9012b54  str x20, [x26,#592]
 6788: 0xf9011356  str x22, [x26,#544]
 6792: 0xf9015b55  str x21, [x26,#688]
 6796: 0xf9014357  str x23, [x26,#640]
 6800: 0xf9014f5c  str x28, [x26,#664]
 6804: 0xd2800060  mov x0, #0x3
 6808: 0xf9002e60  str x0, [x19,#88]
 6812: 0xd28003c0  mov x0, #0x1e
 6816: 0xf9003260  str x0, [x19,#96]
 6820: 0xd28000e0  mov x0, #0x7
 6824: 0xf9003e60  str x0, [x19,#120]
 6828: 0xd2800ae0  mov x0, #0x57
 6832: 0xf9004260  str x0, [x19,#128]
 6836: 0xd28008e0  mov x0, #0x47
 6840: 0xf9004660  str x0, [x19,#136]
 6844: 0xd28000a0  mov x0, #0x5
 6848: 0xf9000a60  str x0, [x19,#16]
 6852: 0x140002b3  b .+0xacc
 6856: 0xf9412b54  ldr x20, [x26,#592]
 6860: 0xf9411356  ldr x22, [x26,#544]
 6864: 0xf9415b55  ldr x21, [x26,#688]
 6868: 0xf9414357  ldr x23, [x26,#640]
 6872: 0xf9414f5c  ldr x28, [x26,#664]
 6876: 0xf9415f40  ldr x0, [x26,#696]
 6880: 0xaa0003fc  mov x28, x0
 6884: 0xf940b740  ldr x0, [x26,#360]
 6888: 0xaa1c03e1  mov x1, x28
 6892: 0xd370fc02  lsr x2, x0, #48
 6896: 0xd29fffc3  mov x3, #0xfffe
 6900: 0xeb03005f  cmp x2, x3
 6904: 0x54000161  b.ne .+0x2c
 6908: 0xd370fc22  lsr x2, x1, #48
 6912: 0xd29fffc3  mov x3, #0xfffe
 6916: 0xeb03005f  cmp x2, x3
 6920: 0x540001e1  b.ne .+0x3c
 6924: 0x9340bc00  sbfx x0, x0, #0, #48
 6928: 0x9340bc21  sbfx x1, x1, #0, #48
 6932: 0x9b017c00  mul x0, x0, x1
 6936: 0xd340bc00  ubfx x0, x0, #0, #48
 6940: 0xaa180000  orr x0, x0, x24
 6944: 0x14000010  b .+0x40
 6948: 0x9e670000  fmov d0, x0
 6952: 0xd370fc22  lsr x2, x1, #48
 6956: 0xd29fffc3  mov x3, #0xfffe
 6960: 0xeb03005f  cmp x2, x3
 6964: 0x54000101  b.ne .+0x20
 6968: 0x9340bc21  sbfx x1, x1, #0, #48
 6972: 0x9e620021  scvtf d1, x1
 6976: 0x14000006  b .+0x18
 6980: 0x9340bc00  sbfx x0, x0, #0, #48
 6984: 0x9e620000  scvtf d0, x0
 6988: 0x9e670021  fmov d1, x1
 6992: 0x14000002  b .+0x8
 6996: 0x9e670021  fmov d1, x1
 7000: 0x1e610800  fmul d0, d0, d1
 7004: 0x9e660000  fmov x0, d0
 7008: 0xaa0003f6  mov x22, x0
 7012: 0xaa1603e0  mov x0, x22
 7016: 0xf940df41  ldr x1, [x26,#440]
 7020: 0xd370fc02  lsr x2, x0, #48
 7024: 0xd29fffc3  mov x3, #0xfffe
 7028: 0xeb03005f  cmp x2, x3
 7032: 0x54000161  b.ne .+0x2c
 7036: 0xd370fc22  lsr x2, x1, #48
 7040: 0xd29fffc3  mov x3, #0xfffe
 7044: 0xeb03005f  cmp x2, x3
 7048: 0x540001e1  b.ne .+0x3c
 7052: 0x9340bc00  sbfx x0, x0, #0, #48
 7056: 0x9340bc21  sbfx x1, x1, #0, #48
 7060: 0x9b017c00  mul x0, x0, x1
 7064: 0xd340bc00  ubfx x0, x0, #0, #48
 7068: 0xaa180000  orr x0, x0, x24
 7072: 0x14000010  b .+0x40
 7076: 0x9e670000  fmov d0, x0
 7080: 0xd370fc22  lsr x2, x1, #48
 7084: 0xd29fffc3  mov x3, #0xfffe
 7088: 0xeb03005f  cmp x2, x3
 7092: 0x54000101  b.ne .+0x20
 7096: 0x9340bc21  sbfx x1, x1, #0, #48
 7100: 0x9e620021  scvtf d1, x1
 7104: 0x14000006  b .+0x18
 7108: 0x9340bc00  sbfx x0, x0, #0, #48
 7112: 0x9e620000  scvtf d0, x0
 7116: 0x9e670021  fmov d1, x1
 7120: 0x14000002  b .+0x8
 7124: 0x9e670021  fmov d1, x1
 7128: 0x1e610800  fmul d0, d0, d1
 7132: 0x9e660000  fmov x0, d0
 7136: 0xaa0003fc  mov x28, x0
 7140: 0xaa1503e0  mov x0, x21
 7144: 0xaa1c03e1  mov x1, x28
 7148: 0xd370fc02  lsr x2, x0, #48
 7152: 0xd29fffc3  mov x3, #0xfffe
 7156: 0xeb03005f  cmp x2, x3
 7160: 0x54000161  b.ne .+0x2c
 7164: 0xd370fc22  lsr x2, x1, #48
 7168: 0xd29fffc3  mov x3, #0xfffe
 7172: 0xeb03005f  cmp x2, x3
 7176: 0x540001e1  b.ne .+0x3c
 7180: 0x9340bc00  sbfx x0, x0, #0, #48
 7184: 0x9340bc21  sbfx x1, x1, #0, #48
 7188: 0x8b010000  add x0, x0, x1
 7192: 0xd340bc00  ubfx x0, x0, #0, #48
 7196: 0xaa180000  orr x0, x0, x24
 7200: 0x14000010  b .+0x40
 7204: 0x9e670000  fmov d0, x0
 7208: 0xd370fc22  lsr x2, x1, #48
 7212: 0xd29fffc3  mov x3, #0xfffe
 7216: 0xeb03005f  cmp x2, x3
 7220: 0x54000101  b.ne .+0x20
 7224: 0x9340bc21  sbfx x1, x1, #0, #48
 7228: 0x9e620021  scvtf d1, x1
 7232: 0x14000006  b .+0x18
 7236: 0x9340bc00  sbfx x0, x0, #0, #48
 7240: 0x9e620000  scvtf d0, x0
 7244: 0x9e670021  fmov d1, x1
 7248: 0x14000002  b .+0x8
 7252: 0x9e670021  fmov d1, x1
 7256: 0x1e612800  fadd d0, d0, d1
 7260: 0x9e660000  fmov x0, d0
 7264: 0xaa0003f6  mov x22, x0
 7268: 0xf9409340  ldr x0, [x26,#288]
 7272: 0xf9009340  str x0, [x26,#288]
 7276: 0xaa1603e0  mov x0, x22
 7280: 0xf901f740  str x0, [x26,#1000]
 7284: 0xf901675c  str x28, [x26,#712]
 7288: 0xf9012b54  str x20, [x26,#592]
 7292: 0xf9016b56  str x22, [x26,#720]
 7296: 0xf9015b55  str x21, [x26,#688]
 7300: 0xf9014357  str x23, [x26,#640]
 7304: 0xd2800080  mov x0, #0x4
 7308: 0xf9002e60  str x0, [x19,#88]
 7312: 0xd2800480  mov x0, #0x24
 7316: 0xf9003260  str x0, [x19,#96]
 7320: 0xd2800120  mov x0, #0x9
 7324: 0xf9003e60  str x0, [x19,#120]
 7328: 0xd2800fa0  mov x0, #0x7d
 7332: 0xf9003a60  str x0, [x19,#112]
 7336: 0xd2800960  mov x0, #0x4b
 7340: 0xf9004660  str x0, [x19,#136]
 7344: 0xd28000a0  mov x0, #0x5
 7348: 0xf9000a60  str x0, [x19,#16]
 7352: 0x14000236  b .+0x8d8
 7356: 0xf9412b54  ldr x20, [x26,#592]
 7360: 0xf9416b56  ldr x22, [x26,#720]
 7364: 0xf9415b55  ldr x21, [x26,#688]
 7368: 0xf9414357  ldr x23, [x26,#640]
 7372: 0xf941675c  ldr x28, [x26,#712]
 7376: 0xf9417740  ldr x0, [x26,#744]
 7380: 0x9340bc14  sbfx x20, x0, #0, #48
 7384: 0x14000001  b .+0x4
 7388: 0x91000695  add x21, x20, #0x1
 7392: 0xd340bea0  ubfx x0, x21, #0, #48
 7396: 0xaa180000  orr x0, x0, x24
 7400: 0xf9017740  str x0, [x26,#744]
 7404: 0xf9406b41  ldr x1, [x26,#208]
 7408: 0x9340bc21  sbfx x1, x1, #0, #48
 7412: 0xeb0102bf  cmp x21, x1
 7416: 0x9a9fc7e0  cset x0, le
 7420: 0xaa190000  orr x0, x0, x25
 7424: 0xaa0003f4  mov x20, x0
 7428: 0x37000094  tbnz w20, #0, .+0x10
 7432: 0xf9418340  ldr x0, [x26,#768]
 7436: 0x9340bc14  sbfx x20, x0, #0, #48
 7440: 0x14000002  b .+0x8
 7444: 0x17fff960  b .+0xffffffffffffe580
 7448: 0x91000695  add x21, x20, #0x1
 7452: 0xd340bea0  ubfx x0, x21, #0, #48
 7456: 0xaa180000  orr x0, x0, x24
 7460: 0xf9018340  str x0, [x26,#768]
 7464: 0xf9406b41  ldr x1, [x26,#208]
 7468: 0x9340bc21  sbfx x1, x1, #0, #48
 7472: 0xeb0102bf  cmp x21, x1
 7476: 0x9a9fc7e0  cset x0, le
 7480: 0xaa190000  orr x0, x0, x25
 7484: 0xaa0003f4  mov x20, x0
 7488: 0x37000054  tbnz w20, #0, .+0x8
 7492: 0x14000002  b .+0x8
 7496: 0x17fff8ed  b .+0xffffffffffffe3b4
 7500: 0xd2800034  mov x20, #0x1
 7504: 0xd340be80  ubfx x0, x20, #0, #48
 7508: 0xaa180000  orr x0, x0, x24
 7512: 0xf9018b40  str x0, [x26,#784]
 7516: 0xd2800015  mov x21, #0x0
 7520: 0xd340bea0  ubfx x0, x21, #0, #48
 7524: 0xaa180000  orr x0, x0, x24
 7528: 0xf9018f40  str x0, [x26,#792]
 7532: 0xaa1503f4  mov x20, x21
 7536: 0x140001f3  b .+0x7cc
 7540: 0x92800000  mov x0, #0xffffffffffffffff
 7544: 0xf900d260  str x0, [x19,#416]
 7548: 0xd2800c80  mov x0, #0x64
 7552: 0xf9002260  str x0, [x19,#64]
 7556: 0xd2800000  mov x0, #0x0
 7560: 0xf9002660  str x0, [x19,#72]
 7564: 0xd2800c20  mov x0, #0x61
 7568: 0xf9002a60  str x0, [x19,#80]
 7572: 0xd2800080  mov x0, #0x4
 7576: 0xf9000a60  str x0, [x19,#16]
 7580: 0x140001fd  b .+0x7f4
 7584: 0xf9419340  ldr x0, [x26,#800]
 7588: 0xaa0003f4  mov x20, x0
 7592: 0xaa1403e0  mov x0, x20
 7596: 0xd370fc01  lsr x1, x0, #48
 7600: 0xd29fffe2  mov x2, #0xffff
 7604: 0xeb02003f  cmp x1, x2
 7608: 0x540004c1  b.ne .+0x98
 7612: 0xd36cfc01  lsr x1, x0, #44
 7616: 0xd28001e2  mov x2, #0xf
 7620: 0x8a020021  and x1, x1, x2
 7624: 0xf100003f  cmp x1, #0x0
 7628: 0x54000421  b.ne .+0x84
 7632: 0xd340ac00  ubfx x0, x0, #0, #44
 7636: 0xb40003e0  cbz x0, .+0x7c
 7640: 0xf9403401  ldr x1, [x0,#104]
 7644: 0xb50003a1  cbnz x1, .+0x74
 7648: 0xf941db41  ldr x1, [x26,#944]
 7652: 0xd370fc22  lsr x2, x1, #48
 7656: 0xd29fffc3  mov x3, #0xfffe
 7660: 0xeb03005f  cmp x2, x3
 7664: 0x54000301  b.ne .+0x60
 7668: 0x9340bc21  sbfx x1, x1, #0, #48
 7672: 0xf100003f  cmp x1, #0x0
 7676: 0x540002ab  b.lt .+0x54
 7680: 0x39422402  ldrb w2, [x0,#137]
 7684: 0xf100045f  cmp x2, #0x1
 7688: 0x54000120  b.eq .+0x24
 7692: 0xb5000222  cbnz x2, .+0x44
 7696: 0xf9400802  ldr x2, [x0,#16]
 7700: 0xeb02003f  cmp x1, x2
 7704: 0x540001ca  b.ge .+0x38
 7708: 0xf9400402  ldr x2, [x0,#8]
 7712: 0xf8617840  ldr x0, [x2,x1,lsl #3]
 7716: 0xaa0003f5  mov x21, x0
 7720: 0x14000021  b .+0x84
 7724: 0xf9404c02  ldr x2, [x0,#152]
 7728: 0xeb02003f  cmp x1, x2
 7732: 0x540000ea  b.ge .+0x1c
 7736: 0xf9404802  ldr x2, [x0,#144]
 7740: 0xf8617840  ldr x0, [x2,x1,lsl #3]
 7744: 0xd340bc00  ubfx x0, x0, #0, #48
 7748: 0xaa180000  orr x0, x0, x24
 7752: 0xaa0003f5  mov x21, x0
 7756: 0x14000018  b .+0x60
 7760: 0xaa1403e0  mov x0, x20
 7764: 0xf9019340  str x0, [x26,#800]
 7768: 0xf941db40  ldr x0, [x26,#944]
 7772: 0xf901db40  str x0, [x26,#944]
 7776: 0xf9019354  str x20, [x26,#800]
 7780: 0xf9019755  str x21, [x26,#808]
 7784: 0xd2800020  mov x0, #0x1
 7788: 0xf9002e60  str x0, [x19,#88]
 7792: 0xd2800c80  mov x0, #0x64
 7796: 0xf9003260  str x0, [x19,#96]
 7800: 0xd2800ec0  mov x0, #0x76
 7804: 0xf9003660  str x0, [x19,#104]
 7808: 0xd2800ca0  mov x0, #0x65
 7812: 0xf9003e60  str x0, [x19,#120]
 7816: 0xd2800c60  mov x0, #0x63
 7820: 0xf9004660  str x0, [x19,#136]
 7824: 0xd28000a0  mov x0, #0x5
 7828: 0xf9000a60  str x0, [x19,#16]
 7832: 0x140001be  b .+0x6f8
 7836: 0xf9419354  ldr x20, [x26,#800]
 7840: 0xf9419755  ldr x21, [x26,#808]
 7844: 0xf9419740  ldr x0, [x26,#808]
 7848: 0xaa0003f5  mov x21, x0
 7852: 0xaa1503e0  mov x0, x21
 7856: 0xf9019740  str x0, [x26,#808]
 7860: 0xf9019354  str x20, [x26,#800]
 7864: 0xf9019755  str x21, [x26,#808]
 7868: 0xd2800060  mov x0, #0x3
 7872: 0xf9002e60  str x0, [x19,#88]
 7876: 0xd2800ca0  mov x0, #0x65
 7880: 0xf9003260  str x0, [x19,#96]
 7884: 0xd2800020  mov x0, #0x1
 7888: 0xf9003e60  str x0, [x19,#120]
 7892: 0xd2800cc0  mov x0, #0x66
 7896: 0xf9004260  str x0, [x19,#128]
 7900: 0xd2800c80  mov x0, #0x64
 7904: 0xf9004660  str x0, [x19,#136]
 7908: 0xd28000a0  mov x0, #0x5
 7912: 0xf9000a60  str x0, [x19,#16]
 7916: 0x140001a9  b .+0x6a4
 7920: 0xf9419354  ldr x20, [x26,#800]
 7924: 0xf9419755  ldr x21, [x26,#808]
 7928: 0xf9419b40  ldr x0, [x26,#816]
 7932: 0xaa0003f4  mov x20, x0
 7936: 0xaa1503e0  mov x0, x21
 7940: 0xf9019740  str x0, [x26,#808]
 7944: 0xf9019b54  str x20, [x26,#816]
 7948: 0xf9019755  str x21, [x26,#808]
 7952: 0xd2800060  mov x0, #0x3
 7956: 0xf9002e60  str x0, [x19,#88]
 7960: 0xd2800ca0  mov x0, #0x65
 7964: 0xf9003260  str x0, [x19,#96]
 7968: 0xd28000c0  mov x0, #0x6
 7972: 0xf9003e60  str x0, [x19,#120]
 7976: 0xd2800ce0  mov x0, #0x67
 7980: 0xf9004260  str x0, [x19,#128]
 7984: 0xd2800ca0  mov x0, #0x65
 7988: 0xf9004660  str x0, [x19,#136]
 7992: 0xd28000a0  mov x0, #0x5
 7996: 0xf9000a60  str x0, [x19,#16]
 8000: 0x14000194  b .+0x650
 8004: 0xf9419b54  ldr x20, [x26,#816]
 8008: 0xf9419755  ldr x21, [x26,#808]
 8012: 0xf9419f40  ldr x0, [x26,#824]
 8016: 0xaa0003f6  mov x22, x0
 8020: 0xf9400340  ldr x0, [x26]
 8024: 0xaa1603e1  mov x1, x22
 8028: 0xd370fc02  lsr x2, x0, #48
 8032: 0xd29fffc3  mov x3, #0xfffe
 8036: 0xeb03005f  cmp x2, x3
 8040: 0x54000161  b.ne .+0x2c
 8044: 0xd370fc22  lsr x2, x1, #48
 8048: 0xd29fffc3  mov x3, #0xfffe
 8052: 0xeb03005f  cmp x2, x3
 8056: 0x540001e1  b.ne .+0x3c
 8060: 0x9340bc00  sbfx x0, x0, #0, #48
 8064: 0x9340bc21  sbfx x1, x1, #0, #48
 8068: 0x9b017c00  mul x0, x0, x1
 8072: 0xd340bc00  ubfx x0, x0, #0, #48
 8076: 0xaa180000  orr x0, x0, x24
 8080: 0x14000010  b .+0x40
 8084: 0x9e670000  fmov d0, x0
 8088: 0xd370fc22  lsr x2, x1, #48
 8092: 0xd29fffc3  mov x3, #0xfffe
 8096: 0xeb03005f  cmp x2, x3
 8100: 0x54000101  b.ne .+0x20
 8104: 0x9340bc21  sbfx x1, x1, #0, #48
 8108: 0x9e620021  scvtf d1, x1
 8112: 0x14000006  b .+0x18
 8116: 0x9340bc00  sbfx x0, x0, #0, #48
 8120: 0x9e620000  scvtf d0, x0
 8124: 0x9e670021  fmov d1, x1
 8128: 0x14000002  b .+0x8
 8132: 0x9e670021  fmov d1, x1
 8136: 0x1e610800  fmul d0, d0, d1
 8140: 0x9e660000  fmov x0, d0
 8144: 0xaa0003f7  mov x23, x0
 8148: 0xaa1403e0  mov x0, x20
 8152: 0xaa1703e1  mov x1, x23
 8156: 0xd370fc02  lsr x2, x0, #48
 8160: 0xd29fffc3  mov x3, #0xfffe
 8164: 0xeb03005f  cmp x2, x3
 8168: 0x54000161  b.ne .+0x2c
 8172: 0xd370fc22  lsr x2, x1, #48
 8176: 0xd29fffc3  mov x3, #0xfffe
 8180: 0xeb03005f  cmp x2, x3
 8184: 0x540001e1  b.ne .+0x3c
 8188: 0x9340bc00  sbfx x0, x0, #0, #48
 8192: 0x9340bc21  sbfx x1, x1, #0, #48
 8196: 0x8b010000  add x0, x0, x1
 8200: 0xd340bc00  ubfx x0, x0, #0, #48
 8204: 0xaa180000  orr x0, x0, x24
 8208: 0x14000010  b .+0x40
 8212: 0x9e670000  fmov d0, x0
 8216: 0xd370fc22  lsr x2, x1, #48
 8220: 0xd29fffc3  mov x3, #0xfffe
 8224: 0xeb03005f  cmp x2, x3
 8228: 0x54000101  b.ne .+0x20
 8232: 0x9340bc21  sbfx x1, x1, #0, #48
 8236: 0x9e620021  scvtf d1, x1
 8240: 0x14000006  b .+0x18
 8244: 0x9340bc00  sbfx x0, x0, #0, #48
 8248: 0x9e620000  scvtf d0, x0
 8252: 0x9e670021  fmov d1, x1
 8256: 0x14000002  b .+0x8
 8260: 0x9e670021  fmov d1, x1
 8264: 0x1e612800  fadd d0, d0, d1
 8268: 0x9e660000  fmov x0, d0
 8272: 0xaa0003f6  mov x22, x0
 8276: 0xaa1503e0  mov x0, x21
 8280: 0xf9019740  str x0, [x26,#808]
 8284: 0xaa1603e0  mov x0, x22
 8288: 0xf901fb40  str x0, [x26,#1008]
 8292: 0xf9019b54  str x20, [x26,#816]
 8296: 0xf9019755  str x21, [x26,#808]
 8300: 0xf901a756  str x22, [x26,#840]
 8304: 0xf901a357  str x23, [x26,#832]
 8308: 0xd2800080  mov x0, #0x4
 8312: 0xf9002e60  str x0, [x19,#88]
 8316: 0xd2800ca0  mov x0, #0x65
 8320: 0xf9003260  str x0, [x19,#96]
 8324: 0xd2800020  mov x0, #0x1
 8328: 0xf9003e60  str x0, [x19,#120]
 8332: 0xd2800fc0  mov x0, #0x7e
 8336: 0xf9003a60  str x0, [x19,#112]
 8340: 0xd2800d20  mov x0, #0x69
 8344: 0xf9004660  str x0, [x19,#136]
 8348: 0xd28000a0  mov x0, #0x5
 8352: 0xf9000a60  str x0, [x19,#16]
 8356: 0x1400013b  b .+0x4ec
 8360: 0xf9419b54  ldr x20, [x26,#816]
 8364: 0xf9419755  ldr x21, [x26,#808]
 8368: 0xf941a756  ldr x22, [x26,#840]
 8372: 0xf941a357  ldr x23, [x26,#832]
 8376: 0xaa1503e0  mov x0, x21
 8380: 0xf9019740  str x0, [x26,#808]
 8384: 0xf9019b54  str x20, [x26,#816]
 8388: 0xf9019755  str x21, [x26,#808]
 8392: 0xf901a756  str x22, [x26,#840]
 8396: 0xf901a357  str x23, [x26,#832]
 8400: 0xd2800060  mov x0, #0x3
 8404: 0xf9002e60  str x0, [x19,#88]
 8408: 0xd2800ca0  mov x0, #0x65
 8412: 0xf9003260  str x0, [x19,#96]
 8416: 0xd2800040  mov x0, #0x2
 8420: 0xf9003e60  str x0, [x19,#120]
 8424: 0xd2800d60  mov x0, #0x6b
 8428: 0xf9004260  str x0, [x19,#128]
 8432: 0xd2800d40  mov x0, #0x6a
 8436: 0xf9004660  str x0, [x19,#136]
 8440: 0xd28000a0  mov x0, #0x5
 8444: 0xf9000a60  str x0, [x19,#16]
 8448: 0x14000124  b .+0x490
 8452: 0xf9419b54  ldr x20, [x26,#816]
 8456: 0xf9419755  ldr x21, [x26,#808]
 8460: 0xf941a756  ldr x22, [x26,#840]
 8464: 0xf941a357  ldr x23, [x26,#832]
 8468: 0xf941af40  ldr x0, [x26,#856]
 8472: 0xaa0003f6  mov x22, x0
 8476: 0xaa1503e0  mov x0, x21
 8480: 0xf9019740  str x0, [x26,#808]
 8484: 0xf9019b54  str x20, [x26,#816]
 8488: 0xf9019755  str x21, [x26,#808]
 8492: 0xf901af56  str x22, [x26,#856]
 8496: 0xf901a357  str x23, [x26,#832]
 8500: 0xd2800060  mov x0, #0x3
 8504: 0xf9002e60  str x0, [x19,#88]
 8508: 0xd2800ca0  mov x0, #0x65
 8512: 0xf9003260  str x0, [x19,#96]
 8516: 0xd2800100  mov x0, #0x8
 8520: 0xf9003e60  str x0, [x19,#120]
 8524: 0xd2800d80  mov x0, #0x6c
 8528: 0xf9004260  str x0, [x19,#128]
 8532: 0xd2800d60  mov x0, #0x6b
 8536: 0xf9004660  str x0, [x19,#136]
 8540: 0xd28000a0  mov x0, #0x5
 8544: 0xf9000a60  str x0, [x19,#16]
 8548: 0x1400010b  b .+0x42c
 8552: 0xf941af56  ldr x22, [x26,#856]
 8556: 0xf941a357  ldr x23, [x26,#832]
 8560: 0xf9419b54  ldr x20, [x26,#816]
 8564: 0xf9419755  ldr x21, [x26,#808]
 8568: 0xf941b340  ldr x0, [x26,#864]
 8572: 0xaa0003f7  mov x23, x0
 8576: 0xf9400340  ldr x0, [x26]
 8580: 0xaa1703e1  mov x1, x23
 8584: 0xd370fc02  lsr x2, x0, #48
 8588: 0xd29fffc3  mov x3, #0xfffe
 8592: 0xeb03005f  cmp x2, x3
 8596: 0x54000161  b.ne .+0x2c
 8600: 0xd370fc22  lsr x2, x1, #48
 8604: 0xd29fffc3  mov x3, #0xfffe
 8608: 0xeb03005f  cmp x2, x3
 8612: 0x540001e1  b.ne .+0x3c
 8616: 0x9340bc00  sbfx x0, x0, #0, #48
 8620: 0x9340bc21  sbfx x1, x1, #0, #48
 8624: 0x9b017c00  mul x0, x0, x1
 8628: 0xd340bc00  ubfx x0, x0, #0, #48
 8632: 0xaa180000  orr x0, x0, x24
 8636: 0x14000010  b .+0x40
 8640: 0x9e670000  fmov d0, x0
 8644: 0xd370fc22  lsr x2, x1, #48
 8648: 0xd29fffc3  mov x3, #0xfffe
 8652: 0xeb03005f  cmp x2, x3
 8656: 0x54000101  b.ne .+0x20
 8660: 0x9340bc21  sbfx x1, x1, #0, #48
 8664: 0x9e620021  scvtf d1, x1
 8668: 0x14000006  b .+0x18
 8672: 0x9340bc00  sbfx x0, x0, #0, #48
 8676: 0x9e620000  scvtf d0, x0
 8680: 0x9e670021  fmov d1, x1
 8684: 0x14000002  b .+0x8
 8688: 0x9e670021  fmov d1, x1
 8692: 0x1e610800  fmul d0, d0, d1
 8696: 0x9e660000  fmov x0, d0
 8700: 0xaa0003fc  mov x28, x0
 8704: 0xaa1603e0  mov x0, x22
 8708: 0xaa1c03e1  mov x1, x28
 8712: 0xd370fc02  lsr x2, x0, #48
 8716: 0xd29fffc3  mov x3, #0xfffe
 8720: 0xeb03005f  cmp x2, x3
 8724: 0x54000161  b.ne .+0x2c
 8728: 0xd370fc22  lsr x2, x1, #48
 8732: 0xd29fffc3  mov x3, #0xfffe
 8736: 0xeb03005f  cmp x2, x3
 8740: 0x540001e1  b.ne .+0x3c
 8744: 0x9340bc00  sbfx x0, x0, #0, #48
 8748: 0x9340bc21  sbfx x1, x1, #0, #48
 8752: 0x8b010000  add x0, x0, x1
 8756: 0xd340bc00  ubfx x0, x0, #0, #48
 8760: 0xaa180000  orr x0, x0, x24
 8764: 0x14000010  b .+0x40
 8768: 0x9e670000  fmov d0, x0
 8772: 0xd370fc22  lsr x2, x1, #48
 8776: 0xd29fffc3  mov x3, #0xfffe
 8780: 0xeb03005f  cmp x2, x3
 8784: 0x54000101  b.ne .+0x20
 8788: 0x9340bc21  sbfx x1, x1, #0, #48
 8792: 0x9e620021  scvtf d1, x1
 8796: 0x14000006  b .+0x18
 8800: 0x9340bc00  sbfx x0, x0, #0, #48
 8804: 0x9e620000  scvtf d0, x0
 8808: 0x9e670021  fmov d1, x1
 8812: 0x14000002  b .+0x8
 8816: 0x9e670021  fmov d1, x1
 8820: 0x1e612800  fadd d0, d0, d1
 8824: 0x9e660000  fmov x0, d0
 8828: 0xaa0003f7  mov x23, x0
 8832: 0xaa1503e0  mov x0, x21
 8836: 0xf9019740  str x0, [x26,#808]
 8840: 0xaa1703e0  mov x0, x23
 8844: 0xf901ff40  str x0, [x26,#1016]
 8848: 0xf9019b54  str x20, [x26,#816]
 8852: 0xf9019755  str x21, [x26,#808]
 8856: 0xf901af56  str x22, [x26,#856]
 8860: 0xf901bb57  str x23, [x26,#880]
 8864: 0xf901b75c  str x28, [x26,#872]
 8868: 0xd2800080  mov x0, #0x4
 8872: 0xf9002e60  str x0, [x19,#88]
 8876: 0xd2800ca0  mov x0, #0x65
 8880: 0xf9003260  str x0, [x19,#96]
 8884: 0xd2800040  mov x0, #0x2
 8888: 0xf9003e60  str x0, [x19,#120]
 8892: 0xd2800fe0  mov x0, #0x7f
 8896: 0xf9003a60  str x0, [x19,#112]
 8900: 0xd2800dc0  mov x0, #0x6e
 8904: 0xf9004660  str x0, [x19,#136]
 8908: 0xd28000a0  mov x0, #0x5
 8912: 0xf9000a60  str x0, [x19,#16]
 8916: 0x140000af  b .+0x2bc
 8920: 0xf941bb57  ldr x23, [x26,#880]
 8924: 0xf941b75c  ldr x28, [x26,#872]
 8928: 0xf9419b54  ldr x20, [x26,#816]
 8932: 0xf9419755  ldr x21, [x26,#808]
 8936: 0xf941af56  ldr x22, [x26,#856]
 8940: 0xaa1503e0  mov x0, x21
 8944: 0xf9019740  str x0, [x26,#808]
 8948: 0xf9019755  str x21, [x26,#808]
 8952: 0xf901af56  str x22, [x26,#856]
 8956: 0xf901bb57  str x23, [x26,#880]
 8960: 0xf901b75c  str x28, [x26,#872]
 8964: 0xf9019b54  str x20, [x26,#816]
 8968: 0xd2800060  mov x0, #0x3
 8972: 0xf9002e60  str x0, [x19,#88]
 8976: 0xd2800ca0  mov x0, #0x65
 8980: 0xf9003260  str x0, [x19,#96]
 8984: 0xd2800060  mov x0, #0x3
 8988: 0xf9003e60  str x0, [x19,#120]
 8992: 0xd2800e00  mov x0, #0x70
 8996: 0xf9004260  str x0, [x19,#128]
 9000: 0xd2800de0  mov x0, #0x6f
 9004: 0xf9004660  str x0, [x19,#136]
 9008: 0xd28000a0  mov x0, #0x5
 9012: 0xf9000a60  str x0, [x19,#16]
 9016: 0x14000096  b .+0x258
 9020: 0xf9419b54  ldr x20, [x26,#816]
 9024: 0xf9419755  ldr x21, [x26,#808]
 9028: 0xf941af56  ldr x22, [x26,#856]
 9032: 0xf941bb57  ldr x23, [x26,#880]
 9036: 0xf941b75c  ldr x28, [x26,#872]
 9040: 0xf941c340  ldr x0, [x26,#896]
 9044: 0xaa0003f7  mov x23, x0
 9048: 0xaa1503e0  mov x0, x21
 9052: 0xf9019740  str x0, [x26,#808]
 9056: 0xf901b75c  str x28, [x26,#872]
 9060: 0xf9019b54  str x20, [x26,#816]
 9064: 0xf9019755  str x21, [x26,#808]
 9068: 0xf901af56  str x22, [x26,#856]
 9072: 0xf901c357  str x23, [x26,#896]
 9076: 0xd2800060  mov x0, #0x3
 9080: 0xf9002e60  str x0, [x19,#88]
 9084: 0xd2800ca0  mov x0, #0x65
 9088: 0xf9003260  str x0, [x19,#96]
 9092: 0xd2800120  mov x0, #0x9
 9096: 0xf9003e60  str x0, [x19,#120]
 9100: 0xd2800e20  mov x0, #0x71
 9104: 0xf9004260  str x0, [x19,#128]
 9108: 0xd2800e00  mov x0, #0x70
 9112: 0xf9004660  str x0, [x19,#136]
 9116: 0xd28000a0  mov x0, #0x5
 9120: 0xf9000a60  str x0, [x19,#16]
 9124: 0x1400007b  b .+0x1ec
 9128: 0xf9419b54  ldr x20, [x26,#816]
 9132: 0xf9419755  ldr x21, [x26,#808]
 9136: 0xf941af56  ldr x22, [x26,#856]
 9140: 0xf941c357  ldr x23, [x26,#896]
 9144: 0xf941b75c  ldr x28, [x26,#872]
 9148: 0xf941c740  ldr x0, [x26,#904]
 9152: 0xaa0003fc  mov x28, x0
 9156: 0xf9400340  ldr x0, [x26]
 9160: 0xaa1c03e1  mov x1, x28
 9164: 0xd370fc02  lsr x2, x0, #48
 9168: 0xd29fffc3  mov x3, #0xfffe
 9172: 0xeb03005f  cmp x2, x3
 9176: 0x54000161  b.ne .+0x2c
 9180: 0xd370fc22  lsr x2, x1, #48
 9184: 0xd29fffc3  mov x3, #0xfffe
 9188: 0xeb03005f  cmp x2, x3
 9192: 0x540001e1  b.ne .+0x3c
 9196: 0x9340bc00  sbfx x0, x0, #0, #48
 9200: 0x9340bc21  sbfx x1, x1, #0, #48
 9204: 0x9b017c00  mul x0, x0, x1
 9208: 0xd340bc00  ubfx x0, x0, #0, #48
 9212: 0xaa180000  orr x0, x0, x24
 9216: 0x14000010  b .+0x40
 9220: 0x9e670000  fmov d0, x0
 9224: 0xd370fc22  lsr x2, x1, #48
 9228: 0xd29fffc3  mov x3, #0xfffe
 9232: 0xeb03005f  cmp x2, x3
 9236: 0x54000101  b.ne .+0x20
 9240: 0x9340bc21  sbfx x1, x1, #0, #48
 9244: 0x9e620021  scvtf d1, x1
 9248: 0x14000006  b .+0x18
 9252: 0x9340bc00  sbfx x0, x0, #0, #48
 9256: 0x9e620000  scvtf d0, x0
 9260: 0x9e670021  fmov d1, x1
 9264: 0x14000002  b .+0x8
 9268: 0x9e670021  fmov d1, x1
 9272: 0x1e610800  fmul d0, d0, d1
 9276: 0x9e660000  fmov x0, d0
 9280: 0xaa0003f4  mov x20, x0
 9284: 0xaa1703e0  mov x0, x23
 9288: 0xaa1403e1  mov x1, x20
 9292: 0xd370fc02  lsr x2, x0, #48
 9296: 0xd29fffc3  mov x3, #0xfffe
 9300: 0xeb03005f  cmp x2, x3
 9304: 0x54000161  b.ne .+0x2c
 9308: 0xd370fc22  lsr x2, x1, #48
 9312: 0xd29fffc3  mov x3, #0xfffe
 9316: 0xeb03005f  cmp x2, x3
 9320: 0x540001e1  b.ne .+0x3c
 9324: 0x9340bc00  sbfx x0, x0, #0, #48
 9328: 0x9340bc21  sbfx x1, x1, #0, #48
 9332: 0x8b010000  add x0, x0, x1
 9336: 0xd340bc00  ubfx x0, x0, #0, #48
 9340: 0xaa180000  orr x0, x0, x24
 9344: 0x14000010  b .+0x40
 9348: 0x9e670000  fmov d0, x0
 9352: 0xd370fc22  lsr x2, x1, #48
 9356: 0xd29fffc3  mov x3, #0xfffe
 9360: 0xeb03005f  cmp x2, x3
 9364: 0x54000101  b.ne .+0x20
 9368: 0x9340bc21  sbfx x1, x1, #0, #48
 9372: 0x9e620021  scvtf d1, x1
 9376: 0x14000006  b .+0x18
 9380: 0x9340bc00  sbfx x0, x0, #0, #48
 9384: 0x9e620000  scvtf d0, x0
 9388: 0x9e670021  fmov d1, x1
 9392: 0x14000002  b .+0x8
 9396: 0x9e670021  fmov d1, x1
 9400: 0x1e612800  fadd d0, d0, d1
 9404: 0x9e660000  fmov x0, d0
 9408: 0xaa0003fc  mov x28, x0
 9412: 0xaa1503e0  mov x0, x21
 9416: 0xf9019740  str x0, [x26,#808]
 9420: 0xaa1c03e0  mov x0, x28
 9424: 0xf9020340  str x0, [x26,#1024]
 9428: 0xf901c357  str x23, [x26,#896]
 9432: 0xf901cf5c  str x28, [x26,#920]
 9436: 0xf901cb54  str x20, [x26,#912]
 9440: 0xf9019755  str x21, [x26,#808]
 9444: 0xf901af56  str x22, [x26,#856]
 9448: 0xd2800080  mov x0, #0x4
 9452: 0xf9002e60  str x0, [x19,#88]
 9456: 0xd2800ca0  mov x0, #0x65
 9460: 0xf9003260  str x0, [x19,#96]
 9464: 0xd2800060  mov x0, #0x3
 9468: 0xf9003e60  str x0, [x19,#120]
 9472: 0xd2801000  mov x0, #0x80
 9476: 0xf9003a60  str x0, [x19,#112]
 9480: 0xd2800e60  mov x0, #0x73
 9484: 0xf9004660  str x0, [x19,#136]
 9488: 0xd28000a0  mov x0, #0x5
 9492: 0xf9000a60  str x0, [x19,#16]
 9496: 0x1400001e  b .+0x78
 9500: 0xf941cb54  ldr x20, [x26,#912]
 9504: 0xf9419755  ldr x21, [x26,#808]
 9508: 0xf941af56  ldr x22, [x26,#856]
 9512: 0xf941c357  ldr x23, [x26,#896]
 9516: 0xf941cf5c  ldr x28, [x26,#920]
 9520: 0xf941db40  ldr x0, [x26,#944]
 9524: 0x9340bc14  sbfx x20, x0, #0, #48
 9528: 0x14000001  b .+0x4
 9532: 0x91000695  add x21, x20, #0x1
 9536: 0xd340bea0  ubfx x0, x21, #0, #48
 9540: 0xaa180000  orr x0, x0, x24
 9544: 0xf901db40  str x0, [x26,#944]
 9548: 0xf9406b41  ldr x1, [x26,#208]
 9552: 0x9340bc21  sbfx x1, x1, #0, #48
 9556: 0xeb0102bf  cmp x21, x1
 9560: 0x9a9fc7e0  cset x0, le
 9564: 0xaa190000  orr x0, x0, x25
 9568: 0xaa0003f4  mov x20, x0
 9572: 0x37000054  tbnz w20, #0, .+0x8
 9576: 0x14000002  b .+0x8
 9580: 0x17fffe02  b .+0xfffffffffffff808
 9584: 0xd2ffff80  mov x0, #0xfffc000000000000
 9588: 0xf9000340  str x0, [x26]
 9592: 0xf9007e60  str x0, [x19,#248]
 9596: 0xf9409661  ldr x1, [x19,#296]
 9600: 0xb50003c1  cbnz x1, .+0x78
 9604: 0x14000001  b .+0x4
 9608: 0xd2800000  mov x0, #0x0
 9612: 0xf9000a60  str x0, [x19,#16]
 9616: 0x6d4627e8  ldp d8, d9, [sp,#96]
 9620: 0x6d472fea  ldp d10, d11, [sp,#112]
 9624: 0xa94573fb  ldp x27, x28, [sp,#80]
 9628: 0xa9446bf9  ldp x25, x26, [sp,#64]
 9632: 0xa94363f7  ldp x23, x24, [sp,#48]
 9636: 0xa9425bf5  ldp x21, x22, [sp,#32]
 9640: 0xa94153f3  ldp x19, x20, [sp,#16]
 9644: 0xa9407bfd  ldp x29, x30, [sp]
 9648: 0x910203ff  add sp, sp, #0x80
 9652: 0xd65f03c0  ret
 9656: 0xd10203ff  sub sp, sp, #0x80
 9660: 0xa9007bfd  stp x29, x30, [sp]
 9664: 0x910003fd  mov x29, sp
 9668: 0xa90153f3  stp x19, x20, [sp,#16]
 9672: 0xa9025bf5  stp x21, x22, [sp,#32]
 9676: 0xa90363f7  stp x23, x24, [sp,#48]
 9680: 0xa9046bf9  stp x25, x26, [sp,#64]
 9684: 0xa90573fb  stp x27, x28, [sp,#80]
 9688: 0x6d0627e8  stp d8, d9, [sp,#96]
 9692: 0x6d072fea  stp d10, d11, [sp,#112]
 9696: 0xaa0003f3  mov x19, x0
 9700: 0xf940027a  ldr x26, [x19]
 9704: 0xf940067b  ldr x27, [x19,#8]
 9708: 0xd2ffffd8  mov x24, #0xfffe000000000000
 9712: 0xd2ffffb9  mov x25, #0xfffd000000000000
 9716: 0x17fff692  b .+0xffffffffffffda48
 9720: 0xd2800000  mov x0, #0x0
 9724: 0xf9000a60  str x0, [x19,#16]
 9728: 0x6d4627e8  ldp d8, d9, [sp,#96]
 9732: 0x6d472fea  ldp d10, d11, [sp,#112]
 9736: 0xa94573fb  ldp x27, x28, [sp,#80]
 9740: 0xa9446bf9  ldp x25, x26, [sp,#64]
 9744: 0xa94363f7  ldp x23, x24, [sp,#48]
 9748: 0xa9425bf5  ldp x21, x22, [sp,#32]
 9752: 0xa94153f3  ldp x19, x20, [sp,#16]
 9756: 0xa9407bfd  ldp x29, x30, [sp]
 9760: 0x910203ff  add sp, sp, #0x80
 9764: 0xd65f03c0  ret
 9768: 0xd10203ff  sub sp, sp, #0x80
 9772: 0xa9007bfd  stp x29, x30, [sp]
 9776: 0x910003fd  mov x29, sp
 9780: 0xa90153f3  stp x19, x20, [sp,#16]
 9784: 0xa9025bf5  stp x21, x22, [sp,#32]
 9788: 0xa90363f7  stp x23, x24, [sp,#48]
 9792: 0xa9046bf9  stp x25, x26, [sp,#64]
 9796: 0xa90573fb  stp x27, x28, [sp,#80]
 9800: 0x6d0627e8  stp d8, d9, [sp,#96]
 9804: 0x6d072fea  stp d10, d11, [sp,#112]
 9808: 0xaa0003f3  mov x19, x0
 9812: 0xf940027a  ldr x26, [x19]
 9816: 0xf940067b  ldr x27, [x19,#8]
 9820: 0xd2ffffd8  mov x24, #0xfffe000000000000
 9824: 0xd2ffffb9  mov x25, #0xfffd000000000000
 9828: 0x17fff683  b .+0xffffffffffffda0c
 9832: 0xd10203ff  sub sp, sp, #0x80
 9836: 0xa9007bfd  stp x29, x30, [sp]
 9840: 0x910003fd  mov x29, sp
 9844: 0xa90153f3  stp x19, x20, [sp,#16]
 9848: 0xa9025bf5  stp x21, x22, [sp,#32]
 9852: 0xa90363f7  stp x23, x24, [sp,#48]
 9856: 0xa9046bf9  stp x25, x26, [sp,#64]
 9860: 0xa90573fb  stp x27, x28, [sp,#80]
 9864: 0x6d0627e8  stp d8, d9, [sp,#96]
 9868: 0x6d072fea  stp d10, d11, [sp,#112]
 9872: 0xaa0003f3  mov x19, x0
 9876: 0xf940027a  ldr x26, [x19]
 9880: 0xf940067b  ldr x27, [x19,#8]
 9884: 0xd2ffffd8  mov x24, #0xfffe000000000000
 9888: 0xd2ffffb9  mov x25, #0xfffd000000000000
 9892: 0x17fff687  b .+0xffffffffffffda1c
 9896: 0xd10203ff  sub sp, sp, #0x80
 9900: 0xa9007bfd  stp x29, x30, [sp]
 9904: 0x910003fd  mov x29, sp
 9908: 0xa90153f3  stp x19, x20, [sp,#16]
 9912: 0xa9025bf5  stp x21, x22, [sp,#32]
 9916: 0xa90363f7  stp x23, x24, [sp,#48]
 9920: 0xa9046bf9  stp x25, x26, [sp,#64]
 9924: 0xa90573fb  stp x27, x28, [sp,#80]
 9928: 0x6d0627e8  stp d8, d9, [sp,#96]
 9932: 0x6d072fea  stp d10, d11, [sp,#112]
 9936: 0xaa0003f3  mov x19, x0
 9940: 0xf940027a  ldr x26, [x19]
 9944: 0xf940067b  ldr x27, [x19,#8]
 9948: 0xd2ffffd8  mov x24, #0xfffe000000000000
 9952: 0xd2ffffb9  mov x25, #0xfffd000000000000
 9956: 0x17fff691  b .+0xffffffffffffda44
 9960: 0xd10203ff  sub sp, sp, #0x80
 9964: 0xa9007bfd  stp x29, x30, [sp]
 9968: 0x910003fd  mov x29, sp
 9972: 0xa90153f3  stp x19, x20, [sp,#16]
 9976: 0xa9025bf5  stp x21, x22, [sp,#32]
 9980: 0xa90363f7  stp x23, x24, [sp,#48]
 9984: 0xa9046bf9  stp x25, x26, [sp,#64]
 9988: 0xa90573fb  stp x27, x28, [sp,#80]
 9992: 0x6d0627e8  stp d8, d9, [sp,#96]
 9996: 0x6d072fea  stp d10, d11, [sp,#112]
10000: 0xaa0003f3  mov x19, x0
10004: 0xf940027a  ldr x26, [x19]
10008: 0xf940067b  ldr x27, [x19,#8]
10012: 0xd2ffffd8  mov x24, #0xfffe000000000000
10016: 0xd2ffffb9  mov x25, #0xfffd000000000000
10020: 0x17fff6c2  b .+0xffffffffffffdb08
10024: 0xd10203ff  sub sp, sp, #0x80
10028: 0xa9007bfd  stp x29, x30, [sp]
10032: 0x910003fd  mov x29, sp
10036: 0xa90153f3  stp x19, x20, [sp,#16]
10040: 0xa9025bf5  stp x21, x22, [sp,#32]
10044: 0xa90363f7  stp x23, x24, [sp,#48]
10048: 0xa9046bf9  stp x25, x26, [sp,#64]
10052: 0xa90573fb  stp x27, x28, [sp,#80]
10056: 0x6d0627e8  stp d8, d9, [sp,#96]
10060: 0x6d072fea  stp d10, d11, [sp,#112]
10064: 0xaa0003f3  mov x19, x0
10068: 0xf940027a  ldr x26, [x19]
10072: 0xf940067b  ldr x27, [x19,#8]
10076: 0xd2ffffd8  mov x24, #0xfffe000000000000
10080: 0xd2ffffb9  mov x25, #0xfffd000000000000
10084: 0x17fff6d7  b .+0xffffffffffffdb5c
10088: 0xd10203ff  sub sp, sp, #0x80
10092: 0xa9007bfd  stp x29, x30, [sp]
10096: 0x910003fd  mov x29, sp
10100: 0xa90153f3  stp x19, x20, [sp,#16]
10104: 0xa9025bf5  stp x21, x22, [sp,#32]
10108: 0xa90363f7  stp x23, x24, [sp,#48]
10112: 0xa9046bf9  stp x25, x26, [sp,#64]
10116: 0xa90573fb  stp x27, x28, [sp,#80]
10120: 0x6d0627e8  stp d8, d9, [sp,#96]
10124: 0x6d072fea  stp d10, d11, [sp,#112]
10128: 0xaa0003f3  mov x19, x0
10132: 0xf940027a  ldr x26, [x19]
10136: 0xf940067b  ldr x27, [x19,#8]
10140: 0xd2ffffd8  mov x24, #0xfffe000000000000
10144: 0xd2ffffb9  mov x25, #0xfffd000000000000
10148: 0x17fff705  b .+0xffffffffffffdc14
10152: 0xd10203ff  sub sp, sp, #0x80
10156: 0xa9007bfd  stp x29, x30, [sp]
10160: 0x910003fd  mov x29, sp
10164: 0xa90153f3  stp x19, x20, [sp,#16]
10168: 0xa9025bf5  stp x21, x22, [sp,#32]
10172: 0xa90363f7  stp x23, x24, [sp,#48]
10176: 0xa9046bf9  stp x25, x26, [sp,#64]
10180: 0xa90573fb  stp x27, x28, [sp,#80]
10184: 0x6d0627e8  stp d8, d9, [sp,#96]
10188: 0x6d072fea  stp d10, d11, [sp,#112]
10192: 0xaa0003f3  mov x19, x0
10196: 0xf940027a  ldr x26, [x19]
10200: 0xf940067b  ldr x27, [x19,#8]
10204: 0xd2ffffd8  mov x24, #0xfffe000000000000
10208: 0xd2ffffb9  mov x25, #0xfffd000000000000
10212: 0x17fff708  b .+0xffffffffffffdc20
10216: 0xd10203ff  sub sp, sp, #0x80
10220: 0xa9007bfd  stp x29, x30, [sp]
10224: 0x910003fd  mov x29, sp
10228: 0xa90153f3  stp x19, x20, [sp,#16]
10232: 0xa9025bf5  stp x21, x22, [sp,#32]
10236: 0xa90363f7  stp x23, x24, [sp,#48]
10240: 0xa9046bf9  stp x25, x26, [sp,#64]
10244: 0xa90573fb  stp x27, x28, [sp,#80]
10248: 0x6d0627e8  stp d8, d9, [sp,#96]
10252: 0x6d072fea  stp d10, d11, [sp,#112]
10256: 0xaa0003f3  mov x19, x0
10260: 0xf940027a  ldr x26, [x19]
10264: 0xf940067b  ldr x27, [x19,#8]
10268: 0xd2ffffd8  mov x24, #0xfffe000000000000
10272: 0xd2ffffb9  mov x25, #0xfffd000000000000
10276: 0x17fff70b  b .+0xffffffffffffdc2c
10280: 0xd10203ff  sub sp, sp, #0x80
10284: 0xa9007bfd  stp x29, x30, [sp]
10288: 0x910003fd  mov x29, sp
10292: 0xa90153f3  stp x19, x20, [sp,#16]
10296: 0xa9025bf5  stp x21, x22, [sp,#32]
10300: 0xa90363f7  stp x23, x24, [sp,#48]
10304: 0xa9046bf9  stp x25, x26, [sp,#64]
10308: 0xa90573fb  stp x27, x28, [sp,#80]
10312: 0x6d0627e8  stp d8, d9, [sp,#96]
10316: 0x6d072fea  stp d10, d11, [sp,#112]
10320: 0xaa0003f3  mov x19, x0
10324: 0xf940027a  ldr x26, [x19]
10328: 0xf940067b  ldr x27, [x19,#8]
10332: 0xd2ffffd8  mov x24, #0xfffe000000000000
10336: 0xd2ffffb9  mov x25, #0xfffd000000000000
10340: 0x17fff72f  b .+0xffffffffffffdcbc
10344: 0xd10203ff  sub sp, sp, #0x80
10348: 0xa9007bfd  stp x29, x30, [sp]
10352: 0x910003fd  mov x29, sp
10356: 0xa90153f3  stp x19, x20, [sp,#16]
10360: 0xa9025bf5  stp x21, x22, [sp,#32]
10364: 0xa90363f7  stp x23, x24, [sp,#48]
10368: 0xa9046bf9  stp x25, x26, [sp,#64]
10372: 0xa90573fb  stp x27, x28, [sp,#80]
10376: 0x6d0627e8  stp d8, d9, [sp,#96]
10380: 0x6d072fea  stp d10, d11, [sp,#112]
10384: 0xaa0003f3  mov x19, x0
10388: 0xf940027a  ldr x26, [x19]
10392: 0xf940067b  ldr x27, [x19,#8]
10396: 0xd2ffffd8  mov x24, #0xfffe000000000000
10400: 0xd2ffffb9  mov x25, #0xfffd000000000000
10404: 0x17fff734  b .+0xffffffffffffdcd0
10408: 0xd10203ff  sub sp, sp, #0x80
10412: 0xa9007bfd  stp x29, x30, [sp]
10416: 0x910003fd  mov x29, sp
10420: 0xa90153f3  stp x19, x20, [sp,#16]
10424: 0xa9025bf5  stp x21, x22, [sp,#32]
10428: 0xa90363f7  stp x23, x24, [sp,#48]
10432: 0xa9046bf9  stp x25, x26, [sp,#64]
10436: 0xa90573fb  stp x27, x28, [sp,#80]
10440: 0x6d0627e8  stp d8, d9, [sp,#96]
10444: 0x6d072fea  stp d10, d11, [sp,#112]
10448: 0xaa0003f3  mov x19, x0
10452: 0xf940027a  ldr x26, [x19]
10456: 0xf940067b  ldr x27, [x19,#8]
10460: 0xd2ffffd8  mov x24, #0xfffe000000000000
10464: 0xd2ffffb9  mov x25, #0xfffd000000000000
10468: 0x17fff759  b .+0xffffffffffffdd64
10472: 0xd10203ff  sub sp, sp, #0x80
10476: 0xa9007bfd  stp x29, x30, [sp]
10480: 0x910003fd  mov x29, sp
10484: 0xa90153f3  stp x19, x20, [sp,#16]
10488: 0xa9025bf5  stp x21, x22, [sp,#32]
10492: 0xa90363f7  stp x23, x24, [sp,#48]
10496: 0xa9046bf9  stp x25, x26, [sp,#64]
10500: 0xa90573fb  stp x27, x28, [sp,#80]
10504: 0x6d0627e8  stp d8, d9, [sp,#96]
10508: 0x6d072fea  stp d10, d11, [sp,#112]
10512: 0xaa0003f3  mov x19, x0
10516: 0xf940027a  ldr x26, [x19]
10520: 0xf940067b  ldr x27, [x19,#8]
10524: 0xd2ffffd8  mov x24, #0xfffe000000000000
10528: 0xd2ffffb9  mov x25, #0xfffd000000000000
10532: 0x17fff75e  b .+0xffffffffffffdd78
10536: 0xd10203ff  sub sp, sp, #0x80
10540: 0xa9007bfd  stp x29, x30, [sp]
10544: 0x910003fd  mov x29, sp
10548: 0xa90153f3  stp x19, x20, [sp,#16]
10552: 0xa9025bf5  stp x21, x22, [sp,#32]
10556: 0xa90363f7  stp x23, x24, [sp,#48]
10560: 0xa9046bf9  stp x25, x26, [sp,#64]
10564: 0xa90573fb  stp x27, x28, [sp,#80]
10568: 0x6d0627e8  stp d8, d9, [sp,#96]
10572: 0x6d072fea  stp d10, d11, [sp,#112]
10576: 0xaa0003f3  mov x19, x0
10580: 0xf940027a  ldr x26, [x19]
10584: 0xf940067b  ldr x27, [x19,#8]
10588: 0xd2ffffd8  mov x24, #0xfffe000000000000
10592: 0xd2ffffb9  mov x25, #0xfffd000000000000
10596: 0x17fff820  b .+0xffffffffffffe080
10600: 0xd10203ff  sub sp, sp, #0x80
10604: 0xa9007bfd  stp x29, x30, [sp]
10608: 0x910003fd  mov x29, sp
10612: 0xa90153f3  stp x19, x20, [sp,#16]
10616: 0xa9025bf5  stp x21, x22, [sp,#32]
10620: 0xa90363f7  stp x23, x24, [sp,#48]
10624: 0xa9046bf9  stp x25, x26, [sp,#64]
10628: 0xa90573fb  stp x27, x28, [sp,#80]
10632: 0x6d0627e8  stp d8, d9, [sp,#96]
10636: 0x6d072fea  stp d10, d11, [sp,#112]
10640: 0xaa0003f3  mov x19, x0
10644: 0xf940027a  ldr x26, [x19]
10648: 0xf940067b  ldr x27, [x19,#8]
10652: 0xd2ffffd8  mov x24, #0xfffe000000000000
10656: 0xd2ffffb9  mov x25, #0xfffd000000000000
10660: 0x17fff827  b .+0xffffffffffffe09c
10664: 0xd10203ff  sub sp, sp, #0x80
10668: 0xa9007bfd  stp x29, x30, [sp]
10672: 0x910003fd  mov x29, sp
10676: 0xa90153f3  stp x19, x20, [sp,#16]
10680: 0xa9025bf5  stp x21, x22, [sp,#32]
10684: 0xa90363f7  stp x23, x24, [sp,#48]
10688: 0xa9046bf9  stp x25, x26, [sp,#64]
10692: 0xa90573fb  stp x27, x28, [sp,#80]
10696: 0x6d0627e8  stp d8, d9, [sp,#96]
10700: 0x6d072fea  stp d10, d11, [sp,#112]
10704: 0xaa0003f3  mov x19, x0
10708: 0xf940027a  ldr x26, [x19]
10712: 0xf940067b  ldr x27, [x19,#8]
10716: 0xd2ffffd8  mov x24, #0xfffe000000000000
10720: 0xd2ffffb9  mov x25, #0xfffd000000000000
10724: 0x17fff87b  b .+0xffffffffffffe1ec
10728: 0xd10203ff  sub sp, sp, #0x80
10732: 0xa9007bfd  stp x29, x30, [sp]
10736: 0x910003fd  mov x29, sp
10740: 0xa90153f3  stp x19, x20, [sp,#16]
10744: 0xa9025bf5  stp x21, x22, [sp,#32]
10748: 0xa90363f7  stp x23, x24, [sp,#48]
10752: 0xa9046bf9  stp x25, x26, [sp,#64]
10756: 0xa90573fb  stp x27, x28, [sp,#80]
10760: 0x6d0627e8  stp d8, d9, [sp,#96]
10764: 0x6d072fea  stp d10, d11, [sp,#112]
10768: 0xaa0003f3  mov x19, x0
10772: 0xf940027a  ldr x26, [x19]
10776: 0xf940067b  ldr x27, [x19,#8]
10780: 0xd2ffffd8  mov x24, #0xfffe000000000000
10784: 0xd2ffffb9  mov x25, #0xfffd000000000000
10788: 0x17fff8b7  b .+0xffffffffffffe2dc
10792: 0xd10203ff  sub sp, sp, #0x80
10796: 0xa9007bfd  stp x29, x30, [sp]
10800: 0x910003fd  mov x29, sp
10804: 0xa90153f3  stp x19, x20, [sp,#16]
10808: 0xa9025bf5  stp x21, x22, [sp,#32]
10812: 0xa90363f7  stp x23, x24, [sp,#48]
10816: 0xa9046bf9  stp x25, x26, [sp,#64]
10820: 0xa90573fb  stp x27, x28, [sp,#80]
10824: 0x6d0627e8  stp d8, d9, [sp,#96]
10828: 0x6d072fea  stp d10, d11, [sp,#112]
10832: 0xaa0003f3  mov x19, x0
10836: 0xf940027a  ldr x26, [x19]
10840: 0xf940067b  ldr x27, [x19,#8]
10844: 0xd2ffffd8  mov x24, #0xfffe000000000000
10848: 0xd2ffffb9  mov x25, #0xfffd000000000000
10852: 0x17fff8be  b .+0xffffffffffffe2f8
10856: 0xd10203ff  sub sp, sp, #0x80
10860: 0xa9007bfd  stp x29, x30, [sp]
10864: 0x910003fd  mov x29, sp
10868: 0xa90153f3  stp x19, x20, [sp,#16]
10872: 0xa9025bf5  stp x21, x22, [sp,#32]
10876: 0xa90363f7  stp x23, x24, [sp,#48]
10880: 0xa9046bf9  stp x25, x26, [sp,#64]
10884: 0xa90573fb  stp x27, x28, [sp,#80]
10888: 0x6d0627e8  stp d8, d9, [sp,#96]
10892: 0x6d072fea  stp d10, d11, [sp,#112]
10896: 0xaa0003f3  mov x19, x0
10900: 0xf940027a  ldr x26, [x19]
10904: 0xf940067b  ldr x27, [x19,#8]
10908: 0xd2ffffd8  mov x24, #0xfffe000000000000
10912: 0xd2ffffb9  mov x25, #0xfffd000000000000
10916: 0x17fff927  b .+0xffffffffffffe49c
10920: 0xd10203ff  sub sp, sp, #0x80
10924: 0xa9007bfd  stp x29, x30, [sp]
10928: 0x910003fd  mov x29, sp
10932: 0xa90153f3  stp x19, x20, [sp,#16]
10936: 0xa9025bf5  stp x21, x22, [sp,#32]
10940: 0xa90363f7  stp x23, x24, [sp,#48]
10944: 0xa9046bf9  stp x25, x26, [sp,#64]
10948: 0xa90573fb  stp x27, x28, [sp,#80]
10952: 0x6d0627e8  stp d8, d9, [sp,#96]
10956: 0x6d072fea  stp d10, d11, [sp,#112]
10960: 0xaa0003f3  mov x19, x0
10964: 0xf940027a  ldr x26, [x19]
10968: 0xf940067b  ldr x27, [x19,#8]
10972: 0xd2ffffd8  mov x24, #0xfffe000000000000
10976: 0xd2ffffb9  mov x25, #0xfffd000000000000
10980: 0x17fff92c  b .+0xffffffffffffe4b0
10984: 0xd10203ff  sub sp, sp, #0x80
10988: 0xa9007bfd  stp x29, x30, [sp]
10992: 0x910003fd  mov x29, sp
10996: 0xa90153f3  stp x19, x20, [sp,#16]
11000: 0xa9025bf5  stp x21, x22, [sp,#32]
11004: 0xa90363f7  stp x23, x24, [sp,#48]
11008: 0xa9046bf9  stp x25, x26, [sp,#64]
11012: 0xa90573fb  stp x27, x28, [sp,#80]
11016: 0x6d0627e8  stp d8, d9, [sp,#96]
11020: 0x6d072fea  stp d10, d11, [sp,#112]
11024: 0xaa0003f3  mov x19, x0
11028: 0xf940027a  ldr x26, [x19]
11032: 0xf940067b  ldr x27, [x19,#8]
11036: 0xd2ffffd8  mov x24, #0xfffe000000000000
11040: 0xd2ffffb9  mov x25, #0xfffd000000000000
11044: 0x17fff933  b .+0xffffffffffffe4cc
11048: 0xd10203ff  sub sp, sp, #0x80
11052: 0xa9007bfd  stp x29, x30, [sp]
11056: 0x910003fd  mov x29, sp
11060: 0xa90153f3  stp x19, x20, [sp,#16]
11064: 0xa9025bf5  stp x21, x22, [sp,#32]
11068: 0xa90363f7  stp x23, x24, [sp,#48]
11072: 0xa9046bf9  stp x25, x26, [sp,#64]
11076: 0xa90573fb  stp x27, x28, [sp,#80]
11080: 0x6d0627e8  stp d8, d9, [sp,#96]
11084: 0x6d072fea  stp d10, d11, [sp,#112]
11088: 0xaa0003f3  mov x19, x0
11092: 0xf940027a  ldr x26, [x19]
11096: 0xf940067b  ldr x27, [x19,#8]
11100: 0xd2ffffd8  mov x24, #0xfffe000000000000
11104: 0xd2ffffb9  mov x25, #0xfffd000000000000
11108: 0x17fff99d  b .+0xffffffffffffe674
11112: 0xd10203ff  sub sp, sp, #0x80
11116: 0xa9007bfd  stp x29, x30, [sp]
11120: 0x910003fd  mov x29, sp
11124: 0xa90153f3  stp x19, x20, [sp,#16]
11128: 0xa9025bf5  stp x21, x22, [sp,#32]
11132: 0xa90363f7  stp x23, x24, [sp,#48]
11136: 0xa9046bf9  stp x25, x26, [sp,#64]
11140: 0xa90573fb  stp x27, x28, [sp,#80]
11144: 0x6d0627e8  stp d8, d9, [sp,#96]
11148: 0x6d072fea  stp d10, d11, [sp,#112]
11152: 0xaa0003f3  mov x19, x0
11156: 0xf940027a  ldr x26, [x19]
11160: 0xf940067b  ldr x27, [x19,#8]
11164: 0xd2ffffd8  mov x24, #0xfffe000000000000
11168: 0xd2ffffb9  mov x25, #0xfffd000000000000
11172: 0x17fff9a4  b .+0xffffffffffffe690
11176: 0xd10203ff  sub sp, sp, #0x80
11180: 0xa9007bfd  stp x29, x30, [sp]
11184: 0x910003fd  mov x29, sp
11188: 0xa90153f3  stp x19, x20, [sp,#16]
11192: 0xa9025bf5  stp x21, x22, [sp,#32]
11196: 0xa90363f7  stp x23, x24, [sp,#48]
11200: 0xa9046bf9  stp x25, x26, [sp,#64]
11204: 0xa90573fb  stp x27, x28, [sp,#80]
11208: 0x6d0627e8  stp d8, d9, [sp,#96]
11212: 0x6d072fea  stp d10, d11, [sp,#112]
11216: 0xaa0003f3  mov x19, x0
11220: 0xf940027a  ldr x26, [x19]
11224: 0xf940067b  ldr x27, [x19,#8]
11228: 0xd2ffffd8  mov x24, #0xfffe000000000000
11232: 0xd2ffffb9  mov x25, #0xfffd000000000000
11236: 0x17fff9ad  b .+0xffffffffffffe6b4
11240: 0xd10203ff  sub sp, sp, #0x80
11244: 0xa9007bfd  stp x29, x30, [sp]
11248: 0x910003fd  mov x29, sp
11252: 0xa90153f3  stp x19, x20, [sp,#16]
11256: 0xa9025bf5  stp x21, x22, [sp,#32]
11260: 0xa90363f7  stp x23, x24, [sp,#48]
11264: 0xa9046bf9  stp x25, x26, [sp,#64]
11268: 0xa90573fb  stp x27, x28, [sp,#80]
11272: 0x6d0627e8  stp d8, d9, [sp,#96]
11276: 0x6d072fea  stp d10, d11, [sp,#112]
11280: 0xaa0003f3  mov x19, x0
11284: 0xf940027a  ldr x26, [x19]
11288: 0xf940067b  ldr x27, [x19,#8]
11292: 0xd2ffffd8  mov x24, #0xfffe000000000000
11296: 0xd2ffffb9  mov x25, #0xfffd000000000000
11300: 0x17fffa18  b .+0xffffffffffffe860
11304: 0xd10203ff  sub sp, sp, #0x80
11308: 0xa9007bfd  stp x29, x30, [sp]
11312: 0x910003fd  mov x29, sp
11316: 0xa90153f3  stp x19, x20, [sp,#16]
11320: 0xa9025bf5  stp x21, x22, [sp,#32]
11324: 0xa90363f7  stp x23, x24, [sp,#48]
11328: 0xa9046bf9  stp x25, x26, [sp,#64]
11332: 0xa90573fb  stp x27, x28, [sp,#80]
11336: 0x6d0627e8  stp d8, d9, [sp,#96]
11340: 0x6d072fea  stp d10, d11, [sp,#112]
11344: 0xaa0003f3  mov x19, x0
11348: 0xf940027a  ldr x26, [x19]
11352: 0xf940067b  ldr x27, [x19,#8]
11356: 0xd2ffffd8  mov x24, #0xfffe000000000000
11360: 0xd2ffffb9  mov x25, #0xfffd000000000000
11364: 0x17fffa1f  b .+0xffffffffffffe87c
11368: 0xd10203ff  sub sp, sp, #0x80
11372: 0xa9007bfd  stp x29, x30, [sp]
11376: 0x910003fd  mov x29, sp
11380: 0xa90153f3  stp x19, x20, [sp,#16]
11384: 0xa9025bf5  stp x21, x22, [sp,#32]
11388: 0xa90363f7  stp x23, x24, [sp,#48]
11392: 0xa9046bf9  stp x25, x26, [sp,#64]
11396: 0xa90573fb  stp x27, x28, [sp,#80]
11400: 0x6d0627e8  stp d8, d9, [sp,#96]
11404: 0x6d072fea  stp d10, d11, [sp,#112]
11408: 0xaa0003f3  mov x19, x0
11412: 0xf940027a  ldr x26, [x19]
11416: 0xf940067b  ldr x27, [x19,#8]
11420: 0xd2ffffd8  mov x24, #0xfffe000000000000
11424: 0xd2ffffb9  mov x25, #0xfffd000000000000
11428: 0x17fffa28  b .+0xffffffffffffe8a0
11432: 0xd10203ff  sub sp, sp, #0x80
11436: 0xa9007bfd  stp x29, x30, [sp]
11440: 0x910003fd  mov x29, sp
11444: 0xa90153f3  stp x19, x20, [sp,#16]
11448: 0xa9025bf5  stp x21, x22, [sp,#32]
11452: 0xa90363f7  stp x23, x24, [sp,#48]
11456: 0xa9046bf9  stp x25, x26, [sp,#64]
11460: 0xa90573fb  stp x27, x28, [sp,#80]
11464: 0x6d0627e8  stp d8, d9, [sp,#96]
11468: 0x6d072fea  stp d10, d11, [sp,#112]
11472: 0xaa0003f3  mov x19, x0
11476: 0xf940027a  ldr x26, [x19]
11480: 0xf940067b  ldr x27, [x19,#8]
11484: 0xd2ffffd8  mov x24, #0xfffe000000000000
11488: 0xd2ffffb9  mov x25, #0xfffd000000000000
11492: 0x17fffa94  b .+0xffffffffffffea50
11496: 0xd10203ff  sub sp, sp, #0x80
11500: 0xa9007bfd  stp x29, x30, [sp]
11504: 0x910003fd  mov x29, sp
11508: 0xa90153f3  stp x19, x20, [sp,#16]
11512: 0xa9025bf5  stp x21, x22, [sp,#32]
11516: 0xa90363f7  stp x23, x24, [sp,#48]
11520: 0xa9046bf9  stp x25, x26, [sp,#64]
11524: 0xa90573fb  stp x27, x28, [sp,#80]
11528: 0x6d0627e8  stp d8, d9, [sp,#96]
11532: 0x6d072fea  stp d10, d11, [sp,#112]
11536: 0xaa0003f3  mov x19, x0
11540: 0xf940027a  ldr x26, [x19]
11544: 0xf940067b  ldr x27, [x19,#8]
11548: 0xd2ffffd8  mov x24, #0xfffe000000000000
11552: 0xd2ffffb9  mov x25, #0xfffd000000000000
11556: 0x17fffa9d  b .+0xffffffffffffea74
11560: 0xd10203ff  sub sp, sp, #0x80
11564: 0xa9007bfd  stp x29, x30, [sp]
11568: 0x910003fd  mov x29, sp
11572: 0xa90153f3  stp x19, x20, [sp,#16]
11576: 0xa9025bf5  stp x21, x22, [sp,#32]
11580: 0xa90363f7  stp x23, x24, [sp,#48]
11584: 0xa9046bf9  stp x25, x26, [sp,#64]
11588: 0xa90573fb  stp x27, x28, [sp,#80]
11592: 0x6d0627e8  stp d8, d9, [sp,#96]
11596: 0x6d072fea  stp d10, d11, [sp,#112]
11600: 0xaa0003f3  mov x19, x0
11604: 0xf940027a  ldr x26, [x19]
11608: 0xf940067b  ldr x27, [x19,#8]
11612: 0xd2ffffd8  mov x24, #0xfffe000000000000
11616: 0xd2ffffb9  mov x25, #0xfffd000000000000
11620: 0x17fffaa8  b .+0xffffffffffffeaa0
11624: 0xd10203ff  sub sp, sp, #0x80
11628: 0xa9007bfd  stp x29, x30, [sp]
11632: 0x910003fd  mov x29, sp
11636: 0xa90153f3  stp x19, x20, [sp,#16]
11640: 0xa9025bf5  stp x21, x22, [sp,#32]
11644: 0xa90363f7  stp x23, x24, [sp,#48]
11648: 0xa9046bf9  stp x25, x26, [sp,#64]
11652: 0xa90573fb  stp x27, x28, [sp,#80]
11656: 0x6d0627e8  stp d8, d9, [sp,#96]
11660: 0x6d072fea  stp d10, d11, [sp,#112]
11664: 0xaa0003f3  mov x19, x0
11668: 0xf940027a  ldr x26, [x19]
11672: 0xf940067b  ldr x27, [x19,#8]
11676: 0xd2ffffd8  mov x24, #0xfffe000000000000
11680: 0xd2ffffb9  mov x25, #0xfffd000000000000
11684: 0x17fffb15  b .+0xffffffffffffec54
11688: 0xd10203ff  sub sp, sp, #0x80
11692: 0xa9007bfd  stp x29, x30, [sp]
11696: 0x910003fd  mov x29, sp
11700: 0xa90153f3  stp x19, x20, [sp,#16]
11704: 0xa9025bf5  stp x21, x22, [sp,#32]
11708: 0xa90363f7  stp x23, x24, [sp,#48]
11712: 0xa9046bf9  stp x25, x26, [sp,#64]
11716: 0xa90573fb  stp x27, x28, [sp,#80]
11720: 0x6d0627e8  stp d8, d9, [sp,#96]
11724: 0x6d072fea  stp d10, d11, [sp,#112]
11728: 0xaa0003f3  mov x19, x0
11732: 0xf940027a  ldr x26, [x19]
11736: 0xf940067b  ldr x27, [x19,#8]
11740: 0xd2ffffd8  mov x24, #0xfffe000000000000
11744: 0xd2ffffb9  mov x25, #0xfffd000000000000
11748: 0x17fffb1e  b .+0xffffffffffffec78
11752: 0xd10203ff  sub sp, sp, #0x80
11756: 0xa9007bfd  stp x29, x30, [sp]
11760: 0x910003fd  mov x29, sp
11764: 0xa90153f3  stp x19, x20, [sp,#16]
11768: 0xa9025bf5  stp x21, x22, [sp,#32]
11772: 0xa90363f7  stp x23, x24, [sp,#48]
11776: 0xa9046bf9  stp x25, x26, [sp,#64]
11780: 0xa90573fb  stp x27, x28, [sp,#80]
11784: 0x6d0627e8  stp d8, d9, [sp,#96]
11788: 0x6d072fea  stp d10, d11, [sp,#112]
11792: 0xaa0003f3  mov x19, x0
11796: 0xf940027a  ldr x26, [x19]
11800: 0xf940067b  ldr x27, [x19,#8]
11804: 0xd2ffffd8  mov x24, #0xfffe000000000000
11808: 0xd2ffffb9  mov x25, #0xfffd000000000000
11812: 0x17fffb29  b .+0xffffffffffffeca4
11816: 0xd10203ff  sub sp, sp, #0x80
11820: 0xa9007bfd  stp x29, x30, [sp]
11824: 0x910003fd  mov x29, sp
11828: 0xa90153f3  stp x19, x20, [sp,#16]
11832: 0xa9025bf5  stp x21, x22, [sp,#32]
11836: 0xa90363f7  stp x23, x24, [sp,#48]
11840: 0xa9046bf9  stp x25, x26, [sp,#64]
11844: 0xa90573fb  stp x27, x28, [sp,#80]
11848: 0x6d0627e8  stp d8, d9, [sp,#96]
11852: 0x6d072fea  stp d10, d11, [sp,#112]
11856: 0xaa0003f3  mov x19, x0
11860: 0xf940027a  ldr x26, [x19]
11864: 0xf940067b  ldr x27, [x19,#8]
11868: 0xd2ffffd8  mov x24, #0xfffe000000000000
11872: 0xd2ffffb9  mov x25, #0xfffd000000000000
11876: 0x17fffb96  b .+0xffffffffffffee58
11880: 0xd10203ff  sub sp, sp, #0x80
11884: 0xa9007bfd  stp x29, x30, [sp]
11888: 0x910003fd  mov x29, sp
11892: 0xa90153f3  stp x19, x20, [sp,#16]
11896: 0xa9025bf5  stp x21, x22, [sp,#32]
11900: 0xa90363f7  stp x23, x24, [sp,#48]
11904: 0xa9046bf9  stp x25, x26, [sp,#64]
11908: 0xa90573fb  stp x27, x28, [sp,#80]
11912: 0x6d0627e8  stp d8, d9, [sp,#96]
11916: 0x6d072fea  stp d10, d11, [sp,#112]
11920: 0xaa0003f3  mov x19, x0
11924: 0xf940027a  ldr x26, [x19]
11928: 0xf940067b  ldr x27, [x19,#8]
11932: 0xd2ffffd8  mov x24, #0xfffe000000000000
11936: 0xd2ffffb9  mov x25, #0xfffd000000000000
11940: 0x17fffbbf  b .+0xffffffffffffeefc
11944: 0xd10203ff  sub sp, sp, #0x80
11948: 0xa9007bfd  stp x29, x30, [sp]
11952: 0x910003fd  mov x29, sp
11956: 0xa90153f3  stp x19, x20, [sp,#16]
11960: 0xa9025bf5  stp x21, x22, [sp,#32]
11964: 0xa90363f7  stp x23, x24, [sp,#48]
11968: 0xa9046bf9  stp x25, x26, [sp,#64]
11972: 0xa90573fb  stp x27, x28, [sp,#80]
11976: 0x6d0627e8  stp d8, d9, [sp,#96]
11980: 0x6d072fea  stp d10, d11, [sp,#112]
11984: 0xaa0003f3  mov x19, x0
11988: 0xf940027a  ldr x26, [x19]
11992: 0xf940067b  ldr x27, [x19,#8]
11996: 0xd2ffffd8  mov x24, #0xfffe000000000000
12000: 0xd2ffffb9  mov x25, #0xfffd000000000000
12004: 0x17fffbee  b .+0xffffffffffffefb8
12008: 0xd10203ff  sub sp, sp, #0x80
12012: 0xa9007bfd  stp x29, x30, [sp]
12016: 0x910003fd  mov x29, sp
12020: 0xa90153f3  stp x19, x20, [sp,#16]
12024: 0xa9025bf5  stp x21, x22, [sp,#32]
12028: 0xa90363f7  stp x23, x24, [sp,#48]
12032: 0xa9046bf9  stp x25, x26, [sp,#64]
12036: 0xa90573fb  stp x27, x28, [sp,#80]
12040: 0x6d0627e8  stp d8, d9, [sp,#96]
12044: 0x6d072fea  stp d10, d11, [sp,#112]
12048: 0xaa0003f3  mov x19, x0
12052: 0xf940027a  ldr x26, [x19]
12056: 0xf940067b  ldr x27, [x19,#8]
12060: 0xd2ffffd8  mov x24, #0xfffe000000000000
12064: 0xd2ffffb9  mov x25, #0xfffd000000000000
12068: 0x17fffbf3  b .+0xffffffffffffefcc
12072: 0xd10203ff  sub sp, sp, #0x80
12076: 0xa9007bfd  stp x29, x30, [sp]
12080: 0x910003fd  mov x29, sp
12084: 0xa90153f3  stp x19, x20, [sp,#16]
12088: 0xa9025bf5  stp x21, x22, [sp,#32]
12092: 0xa90363f7  stp x23, x24, [sp,#48]
12096: 0xa9046bf9  stp x25, x26, [sp,#64]
12100: 0xa90573fb  stp x27, x28, [sp,#80]
12104: 0x6d0627e8  stp d8, d9, [sp,#96]
12108: 0x6d072fea  stp d10, d11, [sp,#112]
12112: 0xaa0003f3  mov x19, x0
12116: 0xf940027a  ldr x26, [x19]
12120: 0xf940067b  ldr x27, [x19,#8]
12124: 0xd2ffffd8  mov x24, #0xfffe000000000000
12128: 0xd2ffffb9  mov x25, #0xfffd000000000000
12132: 0x17fffbf8  b .+0xffffffffffffefe0
12136: 0xd10203ff  sub sp, sp, #0x80
12140: 0xa9007bfd  stp x29, x30, [sp]
12144: 0x910003fd  mov x29, sp
12148: 0xa90153f3  stp x19, x20, [sp,#16]
12152: 0xa9025bf5  stp x21, x22, [sp,#32]
12156: 0xa90363f7  stp x23, x24, [sp,#48]
12160: 0xa9046bf9  stp x25, x26, [sp,#64]
12164: 0xa90573fb  stp x27, x28, [sp,#80]
12168: 0x6d0627e8  stp d8, d9, [sp,#96]
12172: 0x6d072fea  stp d10, d11, [sp,#112]
12176: 0xaa0003f3  mov x19, x0
12180: 0xf940027a  ldr x26, [x19]
12184: 0xf940067b  ldr x27, [x19,#8]
12188: 0xd2ffffd8  mov x24, #0xfffe000000000000
12192: 0xd2ffffb9  mov x25, #0xfffd000000000000
12196: 0x17fffc41  b .+0xfffffffffffff104
12200: 0xd10203ff  sub sp, sp, #0x80
12204: 0xa9007bfd  stp x29, x30, [sp]
12208: 0x910003fd  mov x29, sp
12212: 0xa90153f3  stp x19, x20, [sp,#16]
12216: 0xa9025bf5  stp x21, x22, [sp,#32]
12220: 0xa90363f7  stp x23, x24, [sp,#48]
12224: 0xa9046bf9  stp x25, x26, [sp,#64]
12228: 0xa90573fb  stp x27, x28, [sp,#80]
12232: 0x6d0627e8  stp d8, d9, [sp,#96]
12236: 0x6d072fea  stp d10, d11, [sp,#112]
12240: 0xaa0003f3  mov x19, x0
12244: 0xf940027a  ldr x26, [x19]
12248: 0xf940067b  ldr x27, [x19,#8]
12252: 0xd2ffffd8  mov x24, #0xfffe000000000000
12256: 0xd2ffffb9  mov x25, #0xfffd000000000000
12260: 0x17fffc48  b .+0xfffffffffffff120
12264: 0xd10203ff  sub sp, sp, #0x80
12268: 0xa9007bfd  stp x29, x30, [sp]
12272: 0x910003fd  mov x29, sp
12276: 0xa90153f3  stp x19, x20, [sp,#16]
12280: 0xa9025bf5  stp x21, x22, [sp,#32]
12284: 0xa90363f7  stp x23, x24, [sp,#48]
12288: 0xa9046bf9  stp x25, x26, [sp,#64]
12292: 0xa90573fb  stp x27, x28, [sp,#80]
12296: 0x6d0627e8  stp d8, d9, [sp,#96]
12300: 0x6d072fea  stp d10, d11, [sp,#112]
12304: 0xaa0003f3  mov x19, x0
12308: 0xf940027a  ldr x26, [x19]
12312: 0xf940067b  ldr x27, [x19,#8]
12316: 0xd2ffffd8  mov x24, #0xfffe000000000000
12320: 0xd2ffffb9  mov x25, #0xfffd000000000000
12324: 0x17fffc51  b .+0xfffffffffffff144
12328: 0xd10203ff  sub sp, sp, #0x80
12332: 0xa9007bfd  stp x29, x30, [sp]
12336: 0x910003fd  mov x29, sp
12340: 0xa90153f3  stp x19, x20, [sp,#16]
12344: 0xa9025bf5  stp x21, x22, [sp,#32]
12348: 0xa90363f7  stp x23, x24, [sp,#48]
12352: 0xa9046bf9  stp x25, x26, [sp,#64]
12356: 0xa90573fb  stp x27, x28, [sp,#80]
12360: 0x6d0627e8  stp d8, d9, [sp,#96]
12364: 0x6d072fea  stp d10, d11, [sp,#112]
12368: 0xaa0003f3  mov x19, x0
12372: 0xf940027a  ldr x26, [x19]
12376: 0xf940067b  ldr x27, [x19,#8]
12380: 0xd2ffffd8  mov x24, #0xfffe000000000000
12384: 0xd2ffffb9  mov x25, #0xfffd000000000000
12388: 0x17fffc9d  b .+0xfffffffffffff274
12392: 0xd10203ff  sub sp, sp, #0x80
12396: 0xa9007bfd  stp x29, x30, [sp]
12400: 0x910003fd  mov x29, sp
12404: 0xa90153f3  stp x19, x20, [sp,#16]
12408: 0xa9025bf5  stp x21, x22, [sp,#32]
12412: 0xa90363f7  stp x23, x24, [sp,#48]
12416: 0xa9046bf9  stp x25, x26, [sp,#64]
12420: 0xa90573fb  stp x27, x28, [sp,#80]
12424: 0x6d0627e8  stp d8, d9, [sp,#96]
12428: 0x6d072fea  stp d10, d11, [sp,#112]
12432: 0xaa0003f3  mov x19, x0
12436: 0xf940027a  ldr x26, [x19]
12440: 0xf940067b  ldr x27, [x19,#8]
12444: 0xd2ffffd8  mov x24, #0xfffe000000000000
12448: 0xd2ffffb9  mov x25, #0xfffd000000000000
12452: 0x17fffca6  b .+0xfffffffffffff298
12456: 0xd10203ff  sub sp, sp, #0x80
12460: 0xa9007bfd  stp x29, x30, [sp]
12464: 0x910003fd  mov x29, sp
12468: 0xa90153f3  stp x19, x20, [sp,#16]
12472: 0xa9025bf5  stp x21, x22, [sp,#32]
12476: 0xa90363f7  stp x23, x24, [sp,#48]
12480: 0xa9046bf9  stp x25, x26, [sp,#64]
12484: 0xa90573fb  stp x27, x28, [sp,#80]
12488: 0x6d0627e8  stp d8, d9, [sp,#96]
12492: 0x6d072fea  stp d10, d11, [sp,#112]
12496: 0xaa0003f3  mov x19, x0
12500: 0xf940027a  ldr x26, [x19]
12504: 0xf940067b  ldr x27, [x19,#8]
12508: 0xd2ffffd8  mov x24, #0xfffe000000000000
12512: 0xd2ffffb9  mov x25, #0xfffd000000000000
12516: 0x17fffcb1  b .+0xfffffffffffff2c4
12520: 0xd10203ff  sub sp, sp, #0x80
12524: 0xa9007bfd  stp x29, x30, [sp]
12528: 0x910003fd  mov x29, sp
12532: 0xa90153f3  stp x19, x20, [sp,#16]
12536: 0xa9025bf5  stp x21, x22, [sp,#32]
12540: 0xa90363f7  stp x23, x24, [sp,#48]
12544: 0xa9046bf9  stp x25, x26, [sp,#64]
12548: 0xa90573fb  stp x27, x28, [sp,#80]
12552: 0x6d0627e8  stp d8, d9, [sp,#96]
12556: 0x6d072fea  stp d10, d11, [sp,#112]
12560: 0xaa0003f3  mov x19, x0
12564: 0xf940027a  ldr x26, [x19]
12568: 0xf940067b  ldr x27, [x19,#8]
12572: 0xd2ffffd8  mov x24, #0xfffe000000000000
12576: 0xd2ffffb9  mov x25, #0xfffd000000000000
12580: 0x17fffcfe  b .+0xfffffffffffff3f8
