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
   60: 0xf9400b56  ldr x22, [x26,#16]
   64: 0xaa1603e0  mov x0, x22
   68: 0xd370fc02  lsr x2, x0, #48
   72: 0xd29fffc3  mov x3, #0xfffe
   76: 0xeb03005f  cmp x2, x3
   80: 0x54000081  b.ne .+0x10
   84: 0xaa0003f7  mov x23, x0
   88: 0xf9006f57  str x23, [x26,#216]
   92: 0x14000004  b .+0x10
   96: 0xd2800040  mov x0, #0x2
  100: 0xf9000a60  str x0, [x19,#16]
  104: 0x14000281  b .+0xa04
  108: 0xf9000b56  str x22, [x26,#16]
  112: 0xf9006f57  str x23, [x26,#216]
  116: 0xd2800000  mov x0, #0x0
  120: 0xf9002e60  str x0, [x19,#88]
  124: 0xd2800380  mov x0, #0x1c
  128: 0xf9003260  str x0, [x19,#96]
  132: 0xd2800000  mov x0, #0x0
  136: 0xf9003e60  str x0, [x19,#120]
  140: 0xd2800000  mov x0, #0x0
  144: 0xf9004260  str x0, [x19,#128]
  148: 0xd2800060  mov x0, #0x3
  152: 0xf9004660  str x0, [x19,#136]
  156: 0xd28000a0  mov x0, #0x5
  160: 0xf9000a60  str x0, [x19,#16]
  164: 0x14000272  b .+0x9c8
  168: 0xf9400b56  ldr x22, [x26,#16]
  172: 0xf9406f57  ldr x23, [x26,#216]
  176: 0xf9407340  ldr x0, [x26,#224]
  180: 0xaa0003f6  mov x22, x0
  184: 0xf9007356  str x22, [x26,#224]
  188: 0xd280003c  mov x28, #0x1
  192: 0x9340bee0  sbfx x0, x23, #0, #48
  196: 0xd1000414  sub x20, x0, #0x1
  200: 0x9340be80  sbfx x0, x20, #0, #48
  204: 0xeb14001f  cmp x0, x20
  208: 0x54000120  b.eq .+0x24
  212: 0xf9007356  str x22, [x26,#224]
  216: 0xf9006f57  str x23, [x26,#216]
  220: 0xd340bf80  ubfx x0, x28, #0, #48
  224: 0xaa180000  orr x0, x0, x24
  228: 0xf9007740  str x0, [x26,#232]
  232: 0xd2800040  mov x0, #0x2
  236: 0xf9000a60  str x0, [x19,#16]
  240: 0x1400025f  b .+0x97c
  244: 0xd340be80  ubfx x0, x20, #0, #48
  248: 0xaa180000  orr x0, x0, x24
  252: 0xf9007b40  str x0, [x26,#240]
  256: 0xd280003c  mov x28, #0x1
  260: 0xd340bf80  ubfx x0, x28, #0, #48
  264: 0xaa180000  orr x0, x0, x24
  268: 0xf9007f40  str x0, [x26,#248]
  272: 0x92800015  mov x21, #0xffffffffffffffff
  276: 0xd340bea0  ubfx x0, x21, #0, #48
  280: 0xaa180000  orr x0, x0, x24
  284: 0xf9008340  str x0, [x26,#256]
  288: 0xaa1503f4  mov x20, x21
  292: 0x1400023d  b .+0x8f4
  296: 0xd2800000  mov x0, #0x0
  300: 0xf9002e60  str x0, [x19,#88]
  304: 0xd2800420  mov x0, #0x21
  308: 0xf9003260  str x0, [x19,#96]
  312: 0xd2800000  mov x0, #0x0
  316: 0xf9003e60  str x0, [x19,#120]
  320: 0xd2800000  mov x0, #0x0
  324: 0xf9004260  str x0, [x19,#128]
  328: 0xd2800140  mov x0, #0xa
  332: 0xf9004660  str x0, [x19,#136]
  336: 0xd28000a0  mov x0, #0x5
  340: 0xf9000a60  str x0, [x19,#16]
  344: 0x14000245  b .+0x914
  348: 0xf9408740  ldr x0, [x26,#264]
  352: 0xaa0003f4  mov x20, x0
  356: 0xf9008754  str x20, [x26,#264]
  360: 0xf9400340  ldr x0, [x26]
  364: 0xd370fc01  lsr x1, x0, #48
  368: 0xd29fffe2  mov x2, #0xffff
  372: 0xeb02003f  cmp x1, x2
  376: 0x54000501  b.ne .+0xa0
  380: 0xd36cfc01  lsr x1, x0, #44
  384: 0xd28001e2  mov x2, #0xf
  388: 0x8a020021  and x1, x1, x2
  392: 0xf100003f  cmp x1, #0x0
  396: 0x54000461  b.ne .+0x8c
  400: 0xd340ac00  ubfx x0, x0, #0, #44
  404: 0xb4000420  cbz x0, .+0x84
  408: 0xf9403401  ldr x1, [x0,#104]
  412: 0xb50003e1  cbnz x1, .+0x7c
  416: 0xf940ef41  ldr x1, [x26,#472]
  420: 0xd370fc22  lsr x2, x1, #48
  424: 0xd29fffc3  mov x3, #0xfffe
  428: 0xeb03005f  cmp x2, x3
  432: 0x54000341  b.ne .+0x68
  436: 0x9340bc21  sbfx x1, x1, #0, #48
  440: 0xf100003f  cmp x1, #0x0
  444: 0x540002eb  b.lt .+0x5c
  448: 0x39422402  ldrb w2, [x0,#137]
  452: 0xf100045f  cmp x2, #0x1
  456: 0x54000140  b.eq .+0x28
  460: 0xb5000262  cbnz x2, .+0x4c
  464: 0xf9400802  ldr x2, [x0,#16]
  468: 0xeb02003f  cmp x1, x2
  472: 0x5400020a  b.ge .+0x40
  476: 0xf9400402  ldr x2, [x0,#8]
  480: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  484: 0xaa0003f5  mov x21, x0
  488: 0xf9008b55  str x21, [x26,#272]
  492: 0x14000023  b .+0x8c
  496: 0xf9404c02  ldr x2, [x0,#152]
  500: 0xeb02003f  cmp x1, x2
  504: 0x5400010a  b.ge .+0x20
  508: 0xf9404802  ldr x2, [x0,#144]
  512: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  516: 0xd340bc00  ubfx x0, x0, #0, #48
  520: 0xaa180000  orr x0, x0, x24
  524: 0xaa0003f5  mov x21, x0
  528: 0xf9008b55  str x21, [x26,#272]
  532: 0x14000019  b .+0x64
  536: 0xf9400340  ldr x0, [x26]
  540: 0xf9000340  str x0, [x26]
  544: 0xf940ef40  ldr x0, [x26,#472]
  548: 0xf900ef40  str x0, [x26,#472]
  552: 0xf9008754  str x20, [x26,#264]
  556: 0xf9008b55  str x21, [x26,#272]
  560: 0xd2800020  mov x0, #0x1
  564: 0xf9002e60  str x0, [x19,#88]
  568: 0xd2800000  mov x0, #0x0
  572: 0xf9003260  str x0, [x19,#96]
  576: 0xd2800760  mov x0, #0x3b
  580: 0xf9003660  str x0, [x19,#104]
  584: 0xd2800440  mov x0, #0x22
  588: 0xf9003e60  str x0, [x19,#120]
  592: 0xd28001a0  mov x0, #0xd
  596: 0xf9004660  str x0, [x19,#136]
  600: 0xd28000a0  mov x0, #0x5
  604: 0xf9000a60  str x0, [x19,#16]
  608: 0x14000203  b .+0x80c
  612: 0xf9408754  ldr x20, [x26,#264]
  616: 0xf9408b55  ldr x21, [x26,#272]
  620: 0xf9408b40  ldr x0, [x26,#272]
  624: 0xaa0003f5  mov x21, x0
  628: 0xf9008b55  str x21, [x26,#272]
  632: 0xd2800036  mov x22, #0x1
  636: 0xf9406f40  ldr x0, [x26,#216]
  640: 0x9340bc00  sbfx x0, x0, #0, #48
  644: 0xd1000417  sub x23, x0, #0x1
  648: 0x9340bee0  sbfx x0, x23, #0, #48
  652: 0xeb17001f  cmp x0, x23
  656: 0x54000120  b.eq .+0x24
  660: 0xf9008754  str x20, [x26,#264]
  664: 0xf9008b55  str x21, [x26,#272]
  668: 0xd340bec0  ubfx x0, x22, #0, #48
  672: 0xaa180000  orr x0, x0, x24
  676: 0xf9008f40  str x0, [x26,#280]
  680: 0xd2800040  mov x0, #0x2
  684: 0xf9000a60  str x0, [x19,#16]
  688: 0x140001ef  b .+0x7bc
  692: 0xd340bee0  ubfx x0, x23, #0, #48
  696: 0xaa180000  orr x0, x0, x24
  700: 0xf9009340  str x0, [x26,#288]
  704: 0xd2800036  mov x22, #0x1
  708: 0xd340bec0  ubfx x0, x22, #0, #48
  712: 0xaa180000  orr x0, x0, x24
  716: 0xf9009740  str x0, [x26,#296]
  720: 0x9280001c  mov x28, #0xffffffffffffffff
  724: 0xaa1c03f4  mov x20, x28
  728: 0x1400017c  b .+0x5f0
  732: 0xd2800000  mov x0, #0x0
  736: 0x9e670004  fmov d4, x0
  740: 0x9e660080  fmov x0, d4
  744: 0xf9009f40  str x0, [x26,#312]
  748: 0xd2800034  mov x20, #0x1
  752: 0xf9406f40  ldr x0, [x26,#216]
  756: 0x9340bc00  sbfx x0, x0, #0, #48
  760: 0xd1000415  sub x21, x0, #0x1
  764: 0x9340bea0  sbfx x0, x21, #0, #48
  768: 0xeb15001f  cmp x0, x21
  772: 0x540000e0  b.eq .+0x1c
  776: 0xd340be80  ubfx x0, x20, #0, #48
  780: 0xaa180000  orr x0, x0, x24
  784: 0xf900a340  str x0, [x26,#320]
  788: 0xd2800040  mov x0, #0x2
  792: 0xf9000a60  str x0, [x19,#16]
  796: 0x140001d4  b .+0x750
  800: 0xd340bea0  ubfx x0, x21, #0, #48
  804: 0xaa180000  orr x0, x0, x24
  808: 0xf900a740  str x0, [x26,#328]
  812: 0xd2800034  mov x20, #0x1
  816: 0xd340be80  ubfx x0, x20, #0, #48
  820: 0xaa180000  orr x0, x0, x24
  824: 0xf900ab40  str x0, [x26,#336]
  828: 0x92800016  mov x22, #0xffffffffffffffff
  832: 0x9e660080  fmov x0, d4
  836: 0xf900c740  str x0, [x26,#392]
  840: 0xaa1603f4  mov x20, x22
  844: 0x1400010b  b .+0x42c
  848: 0xf9408b40  ldr x0, [x26,#272]
  852: 0xd370fc01  lsr x1, x0, #48
  856: 0xd29fffe2  mov x2, #0xffff
  860: 0xeb02003f  cmp x1, x2
  864: 0x540004c1  b.ne .+0x98
  868: 0xd36cfc01  lsr x1, x0, #44
  872: 0xd28001e2  mov x2, #0xf
  876: 0x8a020021  and x1, x1, x2
  880: 0xf100003f  cmp x1, #0x0
  884: 0x54000421  b.ne .+0x84
  888: 0xd340ac00  ubfx x0, x0, #0, #44
  892: 0xb40003e0  cbz x0, .+0x7c
  896: 0xf9403401  ldr x1, [x0,#104]
  900: 0xb50003a1  cbnz x1, .+0x74
  904: 0xf940cf41  ldr x1, [x26,#408]
  908: 0xd370fc22  lsr x2, x1, #48
  912: 0xd29fffc3  mov x3, #0xfffe
  916: 0xeb03005f  cmp x2, x3
  920: 0x54000301  b.ne .+0x60
  924: 0x9340bc21  sbfx x1, x1, #0, #48
  928: 0xf100003f  cmp x1, #0x0
  932: 0x540002ab  b.lt .+0x54
  936: 0x39422402  ldrb w2, [x0,#137]
  940: 0xf100045f  cmp x2, #0x1
  944: 0x54000120  b.eq .+0x24
  948: 0xb5000222  cbnz x2, .+0x44
  952: 0xf9400802  ldr x2, [x0,#16]
  956: 0xeb02003f  cmp x1, x2
  960: 0x540001ca  b.ge .+0x38
  964: 0xf9400402  ldr x2, [x0,#8]
  968: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  972: 0xaa0003f4  mov x20, x0
  976: 0x1400001f  b .+0x7c
  980: 0xf9404c02  ldr x2, [x0,#152]
  984: 0xeb02003f  cmp x1, x2
  988: 0x540000ea  b.ge .+0x1c
  992: 0xf9404802  ldr x2, [x0,#144]
  996: 0xf8617840  ldr x0, [x2,x1,lsl #3]
 1000: 0xd340bc00  ubfx x0, x0, #0, #48
 1004: 0xaa180000  orr x0, x0, x24
 1008: 0xaa0003f4  mov x20, x0
 1012: 0x14000016  b .+0x58
 1016: 0xf9408b40  ldr x0, [x26,#272]
 1020: 0xf9008b40  str x0, [x26,#272]
 1024: 0xf940cf40  ldr x0, [x26,#408]
 1028: 0xf900cf40  str x0, [x26,#408]
 1032: 0xf900b354  str x20, [x26,#352]
 1036: 0xd2800020  mov x0, #0x1
 1040: 0xf9002e60  str x0, [x19,#88]
 1044: 0xd2800440  mov x0, #0x22
 1048: 0xf9003260  str x0, [x19,#96]
 1052: 0xd2800660  mov x0, #0x33
 1056: 0xf9003660  str x0, [x19,#104]
 1060: 0xd2800580  mov x0, #0x2c
 1064: 0xf9003e60  str x0, [x19,#120]
 1068: 0xd28003e0  mov x0, #0x1f
 1072: 0xf9004660  str x0, [x19,#136]
 1076: 0xd28000a0  mov x0, #0x5
 1080: 0xf9000a60  str x0, [x19,#16]
 1084: 0x1400018c  b .+0x630
 1088: 0xf940b354  ldr x20, [x26,#352]
 1092: 0xf940b340  ldr x0, [x26,#352]
 1096: 0xaa0003f4  mov x20, x0
 1100: 0xf9400740  ldr x0, [x26,#8]
 1104: 0xd370fc01  lsr x1, x0, #48
 1108: 0xd29fffe2  mov x2, #0xffff
 1112: 0xeb02003f  cmp x1, x2
 1116: 0x540004c1  b.ne .+0x98
 1120: 0xd36cfc01  lsr x1, x0, #44
 1124: 0xd28001e2  mov x2, #0xf
 1128: 0x8a020021  and x1, x1, x2
 1132: 0xf100003f  cmp x1, #0x0
 1136: 0x54000421  b.ne .+0x84
 1140: 0xd340ac00  ubfx x0, x0, #0, #44
 1144: 0xb40003e0  cbz x0, .+0x7c
 1148: 0xf9403401  ldr x1, [x0,#104]
 1152: 0xb50003a1  cbnz x1, .+0x74
 1156: 0xf940cf41  ldr x1, [x26,#408]
 1160: 0xd370fc22  lsr x2, x1, #48
 1164: 0xd29fffc3  mov x3, #0xfffe
 1168: 0xeb03005f  cmp x2, x3
 1172: 0x54000301  b.ne .+0x60
 1176: 0x9340bc21  sbfx x1, x1, #0, #48
 1180: 0xf100003f  cmp x1, #0x0
 1184: 0x540002ab  b.lt .+0x54
 1188: 0x39422402  ldrb w2, [x0,#137]
 1192: 0xf100045f  cmp x2, #0x1
 1196: 0x54000120  b.eq .+0x24
 1200: 0xb5000222  cbnz x2, .+0x44
 1204: 0xf9400802  ldr x2, [x0,#16]
 1208: 0xeb02003f  cmp x1, x2
 1212: 0x540001ca  b.ge .+0x38
 1216: 0xf9400402  ldr x2, [x0,#8]
 1220: 0xf8617840  ldr x0, [x2,x1,lsl #3]
 1224: 0xaa0003f5  mov x21, x0
 1228: 0x14000021  b .+0x84
 1232: 0xf9404c02  ldr x2, [x0,#152]
 1236: 0xeb02003f  cmp x1, x2
 1240: 0x540000ea  b.ge .+0x1c
 1244: 0xf9404802  ldr x2, [x0,#144]
 1248: 0xf8617840  ldr x0, [x2,x1,lsl #3]
 1252: 0xd340bc00  ubfx x0, x0, #0, #48
 1256: 0xaa180000  orr x0, x0, x24
 1260: 0xaa0003f5  mov x21, x0
 1264: 0x14000018  b .+0x60
 1268: 0xf9400740  ldr x0, [x26,#8]
 1272: 0xf9000740  str x0, [x26,#8]
 1276: 0xf940cf40  ldr x0, [x26,#408]
 1280: 0xf900cf40  str x0, [x26,#408]
 1284: 0xf900b354  str x20, [x26,#352]
 1288: 0xf900b755  str x21, [x26,#360]
 1292: 0xd2800020  mov x0, #0x1
 1296: 0xf9002e60  str x0, [x19,#88]
 1300: 0xd2800020  mov x0, #0x1
 1304: 0xf9003260  str x0, [x19,#96]
 1308: 0xd2800660  mov x0, #0x33
 1312: 0xf9003660  str x0, [x19,#104]
 1316: 0xd28005a0  mov x0, #0x2d
 1320: 0xf9003e60  str x0, [x19,#120]
 1324: 0xd2800420  mov x0, #0x21
 1328: 0xf9004660  str x0, [x19,#136]
 1332: 0xd28000a0  mov x0, #0x5
 1336: 0xf9000a60  str x0, [x19,#16]
 1340: 0x1400014c  b .+0x530
 1344: 0xf940b354  ldr x20, [x26,#352]
 1348: 0xf940b755  ldr x21, [x26,#360]
 1352: 0xf940b740  ldr x0, [x26,#360]
 1356: 0xaa0003f5  mov x21, x0
 1360: 0xaa1503e0  mov x0, x21
 1364: 0xd370fc01  lsr x1, x0, #48
 1368: 0xd29fffe2  mov x2, #0xffff
 1372: 0xeb02003f  cmp x1, x2
 1376: 0x540004c1  b.ne .+0x98
 1380: 0xd36cfc01  lsr x1, x0, #44
 1384: 0xd28001e2  mov x2, #0xf
 1388: 0x8a020021  and x1, x1, x2
 1392: 0xf100003f  cmp x1, #0x0
 1396: 0x54000421  b.ne .+0x84
 1400: 0xd340ac00  ubfx x0, x0, #0, #44
 1404: 0xb40003e0  cbz x0, .+0x7c
 1408: 0xf9403401  ldr x1, [x0,#104]
 1412: 0xb50003a1  cbnz x1, .+0x74
 1416: 0xf940df41  ldr x1, [x26,#440]
 1420: 0xd370fc22  lsr x2, x1, #48
 1424: 0xd29fffc3  mov x3, #0xfffe
 1428: 0xeb03005f  cmp x2, x3
 1432: 0x54000301  b.ne .+0x60
 1436: 0x9340bc21  sbfx x1, x1, #0, #48
 1440: 0xf100003f  cmp x1, #0x0
 1444: 0x540002ab  b.lt .+0x54
 1448: 0x39422402  ldrb w2, [x0,#137]
 1452: 0xf100045f  cmp x2, #0x1
 1456: 0x54000120  b.eq .+0x24
 1460: 0xb5000222  cbnz x2, .+0x44
 1464: 0xf9400802  ldr x2, [x0,#16]
 1468: 0xeb02003f  cmp x1, x2
 1472: 0x540001ca  b.ge .+0x38
 1476: 0xf9400402  ldr x2, [x0,#8]
 1480: 0xf8617840  ldr x0, [x2,x1,lsl #3]
 1484: 0xaa0003f6  mov x22, x0
 1488: 0x14000023  b .+0x8c
 1492: 0xf9404c02  ldr x2, [x0,#152]
 1496: 0xeb02003f  cmp x1, x2
 1500: 0x540000ea  b.ge .+0x1c
 1504: 0xf9404802  ldr x2, [x0,#144]
 1508: 0xf8617840  ldr x0, [x2,x1,lsl #3]
 1512: 0xd340bc00  ubfx x0, x0, #0, #48
 1516: 0xaa180000  orr x0, x0, x24
 1520: 0xaa0003f6  mov x22, x0
 1524: 0x1400001a  b .+0x68
 1528: 0xaa1503e0  mov x0, x21
 1532: 0xf900b740  str x0, [x26,#360]
 1536: 0xf940df40  ldr x0, [x26,#440]
 1540: 0xf900df40  str x0, [x26,#440]
 1544: 0xf900b354  str x20, [x26,#352]
 1548: 0xf900b755  str x21, [x26,#360]
 1552: 0xf900bb56  str x22, [x26,#368]
 1556: 0xd2800020  mov x0, #0x1
 1560: 0xf9002e60  str x0, [x19,#88]
 1564: 0xd28005a0  mov x0, #0x2d
 1568: 0xf9003260  str x0, [x19,#96]
 1572: 0xd28006e0  mov x0, #0x37
 1576: 0xf9003660  str x0, [x19,#104]
 1580: 0xd28005c0  mov x0, #0x2e
 1584: 0xf9003e60  str x0, [x19,#120]
 1588: 0xd2800460  mov x0, #0x23
 1592: 0xf9004660  str x0, [x19,#136]
 1596: 0xd28000a0  mov x0, #0x5
 1600: 0xf9000a60  str x0, [x19,#16]
 1604: 0x1400010a  b .+0x428
 1608: 0xf940b354  ldr x20, [x26,#352]
 1612: 0xf940b755  ldr x21, [x26,#360]
 1616: 0xf940bb56  ldr x22, [x26,#368]
 1620: 0xf940bb40  ldr x0, [x26,#368]
 1624: 0xaa0003f6  mov x22, x0
 1628: 0xaa1403e0  mov x0, x20
 1632: 0xaa1603e1  mov x1, x22
 1636: 0xd370fc02  lsr x2, x0, #48
 1640: 0xd29fffc3  mov x3, #0xfffe
 1644: 0xeb03005f  cmp x2, x3
 1648: 0x54000161  b.ne .+0x2c
 1652: 0xd370fc22  lsr x2, x1, #48
 1656: 0xd29fffc3  mov x3, #0xfffe
 1660: 0xeb03005f  cmp x2, x3
 1664: 0x540001e1  b.ne .+0x3c
 1668: 0x9340bc00  sbfx x0, x0, #0, #48
 1672: 0x9340bc21  sbfx x1, x1, #0, #48
 1676: 0x9b017c00  mul x0, x0, x1
 1680: 0xd340bc00  ubfx x0, x0, #0, #48
 1684: 0xaa180000  orr x0, x0, x24
 1688: 0x14000010  b .+0x40
 1692: 0x9e670000  fmov d0, x0
 1696: 0xd370fc22  lsr x2, x1, #48
 1700: 0xd29fffc3  mov x3, #0xfffe
 1704: 0xeb03005f  cmp x2, x3
 1708: 0x54000101  b.ne .+0x20
 1712: 0x9340bc21  sbfx x1, x1, #0, #48
 1716: 0x9e620021  scvtf d1, x1
 1720: 0x14000006  b .+0x18
 1724: 0x9340bc00  sbfx x0, x0, #0, #48
 1728: 0x9e620000  scvtf d0, x0
 1732: 0x9e670021  fmov d1, x1
 1736: 0x14000002  b .+0x8
 1740: 0x9e670021  fmov d1, x1
 1744: 0x1e610800  fmul d0, d0, d1
 1748: 0x9e660000  fmov x0, d0
 1752: 0xaa0003f5  mov x21, x0
 1756: 0x9e660080  fmov x0, d4
 1760: 0xaa1503e1  mov x1, x21
 1764: 0xd370fc02  lsr x2, x0, #48
 1768: 0xd29fffc3  mov x3, #0xfffe
 1772: 0xeb03005f  cmp x2, x3
 1776: 0x54000161  b.ne .+0x2c
 1780: 0xd370fc22  lsr x2, x1, #48
 1784: 0xd29fffc3  mov x3, #0xfffe
 1788: 0xeb03005f  cmp x2, x3
 1792: 0x540001e1  b.ne .+0x3c
 1796: 0x9340bc00  sbfx x0, x0, #0, #48
 1800: 0x9340bc21  sbfx x1, x1, #0, #48
 1804: 0x8b010000  add x0, x0, x1
 1808: 0xd340bc00  ubfx x0, x0, #0, #48
 1812: 0xaa180000  orr x0, x0, x24
 1816: 0x14000010  b .+0x40
 1820: 0x9e670000  fmov d0, x0
 1824: 0xd370fc22  lsr x2, x1, #48
 1828: 0xd29fffc3  mov x3, #0xfffe
 1832: 0xeb03005f  cmp x2, x3
 1836: 0x54000101  b.ne .+0x20
 1840: 0x9340bc21  sbfx x1, x1, #0, #48
 1844: 0x9e620021  scvtf d1, x1
 1848: 0x14000006  b .+0x18
 1852: 0x9340bc00  sbfx x0, x0, #0, #48
 1856: 0x9e620000  scvtf d0, x0
 1860: 0x9e670021  fmov d1, x1
 1864: 0x14000002  b .+0x8
 1868: 0x9e670021  fmov d1, x1
 1872: 0x1e612800  fadd d0, d0, d1
 1876: 0x9e660000  fmov x0, d0
 1880: 0xaa0003f4  mov x20, x0
 1884: 0xf900c354  str x20, [x26,#384]
 1888: 0x9e670284  fmov d4, x20
 1892: 0x9e660080  fmov x0, d4
 1896: 0xf900c740  str x0, [x26,#392]
 1900: 0xf940cf40  ldr x0, [x26,#408]
 1904: 0x9340bc14  sbfx x20, x0, #0, #48
 1908: 0x14000001  b .+0x4
 1912: 0x91000695  add x21, x20, #0x1
 1916: 0xd340bea0  ubfx x0, x21, #0, #48
 1920: 0xaa180000  orr x0, x0, x24
 1924: 0xf900cf40  str x0, [x26,#408]
 1928: 0xf940a741  ldr x1, [x26,#328]
 1932: 0x9340bc21  sbfx x1, x1, #0, #48
 1936: 0xeb0102bf  cmp x21, x1
 1940: 0x9a9fc7e0  cset x0, le
 1944: 0xaa190000  orr x0, x0, x25
 1948: 0xaa0003f4  mov x20, x0
 1952: 0x37000054  tbnz w20, #0, .+0x8
 1956: 0x14000002  b .+0x8
 1960: 0x17fffeea  b .+0xfffffffffffffba8
 1964: 0xf9408740  ldr x0, [x26,#264]
 1968: 0xd370fc01  lsr x1, x0, #48
 1972: 0xd29fffe2  mov x2, #0xffff
 1976: 0xeb02003f  cmp x1, x2
 1980: 0x540005a1  b.ne .+0xb4
 1984: 0xd36cfc01  lsr x1, x0, #44
 1988: 0xd28001e2  mov x2, #0xf
 1992: 0x8a020021  and x1, x1, x2
 1996: 0xf100003f  cmp x1, #0x0
 2000: 0x54000501  b.ne .+0xa0
 2004: 0xd340ac00  ubfx x0, x0, #0, #44
 2008: 0xb40004c0  cbz x0, .+0x98
 2012: 0xf9403401  ldr x1, [x0,#104]
 2016: 0xb5000481  cbnz x1, .+0x90
 2020: 0xf940df41  ldr x1, [x26,#440]
 2024: 0xd370fc22  lsr x2, x1, #48
 2028: 0xd29fffc3  mov x3, #0xfffe
 2032: 0xeb03005f  cmp x2, x3
 2036: 0x540003e1  b.ne .+0x7c
 2040: 0x9340bc21  sbfx x1, x1, #0, #48
 2044: 0xf100003f  cmp x1, #0x0
 2048: 0x5400038b  b.lt .+0x70
 2052: 0x39422402  ldrb w2, [x0,#137]
 2056: 0xf100045f  cmp x2, #0x1
 2060: 0x54000160  b.eq .+0x2c
 2064: 0xb5000302  cbnz x2, .+0x60
 2068: 0xf9400802  ldr x2, [x0,#16]
 2072: 0xeb02003f  cmp x1, x2
 2076: 0x540002aa  b.ge .+0x54
 2080: 0xf940c744  ldr x4, [x26,#392]
 2084: 0xf9400402  ldr x2, [x0,#8]
 2088: 0xf8217844  str x4, [x2,x1,lsl #3]
 2092: 0xd2800025  mov x5, #0x1
 2096: 0x39022005  strb w5, [x0,#136]
 2100: 0x14000022  b .+0x88
 2104: 0xf9404c02  ldr x2, [x0,#152]
 2108: 0xeb02003f  cmp x1, x2
 2112: 0x5400018a  b.ge .+0x30
 2116: 0xf940c744  ldr x4, [x26,#392]
 2120: 0xd370fc85  lsr x5, x4, #48
 2124: 0xd29fffc6  mov x6, #0xfffe
 2128: 0xeb0600bf  cmp x5, x6
 2132: 0x540000e1  b.ne .+0x1c
 2136: 0x9340bc84  sbfx x4, x4, #0, #48
 2140: 0xf9404802  ldr x2, [x0,#144]
 2144: 0xf8217844  str x4, [x2,x1,lsl #3]
 2148: 0xd2800025  mov x5, #0x1
 2152: 0x39022005  strb w5, [x0,#136]
 2156: 0x14000014  b .+0x50
 2160: 0xf9408740  ldr x0, [x26,#264]
 2164: 0xf9008740  str x0, [x26,#264]
 2168: 0xf940df40  ldr x0, [x26,#440]
 2172: 0xf900df40  str x0, [x26,#440]
 2176: 0xf940c740  ldr x0, [x26,#392]
 2180: 0xf900c740  str x0, [x26,#392]
 2184: 0xd2800040  mov x0, #0x2
 2188: 0xf9002e60  str x0, [x19,#88]
 2192: 0xd2800420  mov x0, #0x21
 2196: 0xf9003260  str x0, [x19,#96]
 2200: 0xd28006e0  mov x0, #0x37
 2204: 0xf9003660  str x0, [x19,#104]
 2208: 0xd2800620  mov x0, #0x31
 2212: 0xf9003a60  str x0, [x19,#112]
 2216: 0xd28006a0  mov x0, #0x35
 2220: 0xf9004660  str x0, [x19,#136]
 2224: 0xd28000a0  mov x0, #0x5
 2228: 0xf9000a60  str x0, [x19,#16]
 2232: 0x1400006d  b .+0x1b4
 2236: 0xf940df40  ldr x0, [x26,#440]
 2240: 0x9340bc14  sbfx x20, x0, #0, #48
 2244: 0x14000001  b .+0x4
 2248: 0x91000695  add x21, x20, #0x1
 2252: 0xd340bea0  ubfx x0, x21, #0, #48
 2256: 0xaa180000  orr x0, x0, x24
 2260: 0xf900df40  str x0, [x26,#440]
 2264: 0xf9409341  ldr x1, [x26,#288]
 2268: 0x9340bc21  sbfx x1, x1, #0, #48
 2272: 0xeb0102bf  cmp x21, x1
 2276: 0x9a9fc7e0  cset x0, le
 2280: 0xaa190000  orr x0, x0, x25
 2284: 0xaa0003f4  mov x20, x0
 2288: 0x37000054  tbnz w20, #0, .+0x8
 2292: 0x14000002  b .+0x8
 2296: 0x17fffe79  b .+0xfffffffffffff9e4
 2300: 0xf9407340  ldr x0, [x26,#224]
 2304: 0xd370fc01  lsr x1, x0, #48
 2308: 0xd29fffe2  mov x2, #0xffff
 2312: 0xeb02003f  cmp x1, x2
 2316: 0x540005a1  b.ne .+0xb4
 2320: 0xd36cfc01  lsr x1, x0, #44
 2324: 0xd28001e2  mov x2, #0xf
 2328: 0x8a020021  and x1, x1, x2
 2332: 0xf100003f  cmp x1, #0x0
 2336: 0x54000501  b.ne .+0xa0
 2340: 0xd340ac00  ubfx x0, x0, #0, #44
 2344: 0xb40004c0  cbz x0, .+0x98
 2348: 0xf9403401  ldr x1, [x0,#104]
 2352: 0xb5000481  cbnz x1, .+0x90
 2356: 0xf940ef41  ldr x1, [x26,#472]
 2360: 0xd370fc22  lsr x2, x1, #48
 2364: 0xd29fffc3  mov x3, #0xfffe
 2368: 0xeb03005f  cmp x2, x3
 2372: 0x540003e1  b.ne .+0x7c
 2376: 0x9340bc21  sbfx x1, x1, #0, #48
 2380: 0xf100003f  cmp x1, #0x0
 2384: 0x5400038b  b.lt .+0x70
 2388: 0x39422402  ldrb w2, [x0,#137]
 2392: 0xf100045f  cmp x2, #0x1
 2396: 0x54000160  b.eq .+0x2c
 2400: 0xb5000302  cbnz x2, .+0x60
 2404: 0xf9400802  ldr x2, [x0,#16]
 2408: 0xeb02003f  cmp x1, x2
 2412: 0x540002aa  b.ge .+0x54
 2416: 0xf9408744  ldr x4, [x26,#264]
 2420: 0xf9400402  ldr x2, [x0,#8]
 2424: 0xf8217844  str x4, [x2,x1,lsl #3]
 2428: 0xd2800025  mov x5, #0x1
 2432: 0x39022005  strb w5, [x0,#136]
 2436: 0x14000022  b .+0x88
 2440: 0xf9404c02  ldr x2, [x0,#152]
 2444: 0xeb02003f  cmp x1, x2
 2448: 0x5400018a  b.ge .+0x30
 2452: 0xf9408744  ldr x4, [x26,#264]
 2456: 0xd370fc85  lsr x5, x4, #48
 2460: 0xd29fffc6  mov x6, #0xfffe
 2464: 0xeb0600bf  cmp x5, x6
 2468: 0x540000e1  b.ne .+0x1c
 2472: 0x9340bc84  sbfx x4, x4, #0, #48
 2476: 0xf9404802  ldr x2, [x0,#144]
 2480: 0xf8217844  str x4, [x2,x1,lsl #3]
 2484: 0xd2800025  mov x5, #0x1
 2488: 0x39022005  strb w5, [x0,#136]
 2492: 0x14000014  b .+0x50
 2496: 0xf9407340  ldr x0, [x26,#224]
 2500: 0xf9007340  str x0, [x26,#224]
 2504: 0xf940ef40  ldr x0, [x26,#472]
 2508: 0xf900ef40  str x0, [x26,#472]
 2512: 0xf9408740  ldr x0, [x26,#264]
 2516: 0xf9008740  str x0, [x26,#264]
 2520: 0xd2800040  mov x0, #0x2
 2524: 0xf9002e60  str x0, [x19,#88]
 2528: 0xd2800380  mov x0, #0x1c
 2532: 0xf9003260  str x0, [x19,#96]
 2536: 0xd2800760  mov x0, #0x3b
 2540: 0xf9003660  str x0, [x19,#104]
 2544: 0xd2800420  mov x0, #0x21
 2548: 0xf9003a60  str x0, [x19,#112]
 2552: 0xd2800820  mov x0, #0x41
 2556: 0xf9004660  str x0, [x19,#136]
 2560: 0xd28000a0  mov x0, #0x5
 2564: 0xf9000a60  str x0, [x19,#16]
 2568: 0x14000019  b .+0x64
 2572: 0xf940ef40  ldr x0, [x26,#472]
 2576: 0x9340bc14  sbfx x20, x0, #0, #48
 2580: 0x14000001  b .+0x4
 2584: 0x91000695  add x21, x20, #0x1
 2588: 0xd340bea0  ubfx x0, x21, #0, #48
 2592: 0xaa180000  orr x0, x0, x24
 2596: 0xf900ef40  str x0, [x26,#472]
 2600: 0xf9407b41  ldr x1, [x26,#240]
 2604: 0x9340bc21  sbfx x1, x1, #0, #48
 2608: 0xeb0102bf  cmp x21, x1
 2612: 0x9a9fc7e0  cset x0, le
 2616: 0xaa190000  orr x0, x0, x25
 2620: 0xaa0003f4  mov x20, x0
 2624: 0x37000054  tbnz w20, #0, .+0x8
 2628: 0x14000002  b .+0x8
 2632: 0x17fffdb8  b .+0xfffffffffffff6e0
 2636: 0xf9407340  ldr x0, [x26,#224]
 2640: 0xf9000340  str x0, [x26]
 2644: 0xf9007e60  str x0, [x19,#248]
 2648: 0xf9409661  ldr x1, [x19,#296]
 2652: 0xb50003c1  cbnz x1, .+0x78
 2656: 0x14000001  b .+0x4
 2660: 0xd2800000  mov x0, #0x0
 2664: 0xf9000a60  str x0, [x19,#16]
 2668: 0x6d4627e8  ldp d8, d9, [sp,#96]
 2672: 0x6d472fea  ldp d10, d11, [sp,#112]
 2676: 0xa94573fb  ldp x27, x28, [sp,#80]
 2680: 0xa9446bf9  ldp x25, x26, [sp,#64]
 2684: 0xa94363f7  ldp x23, x24, [sp,#48]
 2688: 0xa9425bf5  ldp x21, x22, [sp,#32]
 2692: 0xa94153f3  ldp x19, x20, [sp,#16]
 2696: 0xa9407bfd  ldp x29, x30, [sp]
 2700: 0x910203ff  add sp, sp, #0x80
 2704: 0xd65f03c0  ret
 2708: 0xd10203ff  sub sp, sp, #0x80
 2712: 0xa9007bfd  stp x29, x30, [sp]
 2716: 0x910003fd  mov x29, sp
 2720: 0xa90153f3  stp x19, x20, [sp,#16]
 2724: 0xa9025bf5  stp x21, x22, [sp,#32]
 2728: 0xa90363f7  stp x23, x24, [sp,#48]
 2732: 0xa9046bf9  stp x25, x26, [sp,#64]
 2736: 0xa90573fb  stp x27, x28, [sp,#80]
 2740: 0x6d0627e8  stp d8, d9, [sp,#96]
 2744: 0x6d072fea  stp d10, d11, [sp,#112]
 2748: 0xaa0003f3  mov x19, x0
 2752: 0xf940027a  ldr x26, [x19]
 2756: 0xf940067b  ldr x27, [x19,#8]
 2760: 0xd2ffffd8  mov x24, #0xfffe000000000000
 2764: 0xd2ffffb9  mov x25, #0xfffd000000000000
 2768: 0x17fffd5b  b .+0xfffffffffffff56c
 2772: 0xd2800000  mov x0, #0x0
 2776: 0xf9000a60  str x0, [x19,#16]
 2780: 0x6d4627e8  ldp d8, d9, [sp,#96]
 2784: 0x6d472fea  ldp d10, d11, [sp,#112]
 2788: 0xa94573fb  ldp x27, x28, [sp,#80]
 2792: 0xa9446bf9  ldp x25, x26, [sp,#64]
 2796: 0xa94363f7  ldp x23, x24, [sp,#48]
 2800: 0xa9425bf5  ldp x21, x22, [sp,#32]
 2804: 0xa94153f3  ldp x19, x20, [sp,#16]
 2808: 0xa9407bfd  ldp x29, x30, [sp]
 2812: 0x910203ff  add sp, sp, #0x80
 2816: 0xd65f03c0  ret
 2820: 0xd10203ff  sub sp, sp, #0x80
 2824: 0xa9007bfd  stp x29, x30, [sp]
 2828: 0x910003fd  mov x29, sp
 2832: 0xa90153f3  stp x19, x20, [sp,#16]
 2836: 0xa9025bf5  stp x21, x22, [sp,#32]
 2840: 0xa90363f7  stp x23, x24, [sp,#48]
 2844: 0xa9046bf9  stp x25, x26, [sp,#64]
 2848: 0xa90573fb  stp x27, x28, [sp,#80]
 2852: 0x6d0627e8  stp d8, d9, [sp,#96]
 2856: 0x6d072fea  stp d10, d11, [sp,#112]
 2860: 0xaa0003f3  mov x19, x0
 2864: 0xf940027a  ldr x26, [x19]
 2868: 0xf940067b  ldr x27, [x19,#8]
 2872: 0xd2ffffd8  mov x24, #0xfffe000000000000
 2876: 0xd2ffffb9  mov x25, #0xfffd000000000000
 2880: 0x17fffd5a  b .+0xfffffffffffff568
 2884: 0xd10203ff  sub sp, sp, #0x80
 2888: 0xa9007bfd  stp x29, x30, [sp]
 2892: 0x910003fd  mov x29, sp
 2896: 0xa90153f3  stp x19, x20, [sp,#16]
 2900: 0xa9025bf5  stp x21, x22, [sp,#32]
 2904: 0xa90363f7  stp x23, x24, [sp,#48]
 2908: 0xa9046bf9  stp x25, x26, [sp,#64]
 2912: 0xa90573fb  stp x27, x28, [sp,#80]
 2916: 0x6d0627e8  stp d8, d9, [sp,#96]
 2920: 0x6d072fea  stp d10, d11, [sp,#112]
 2924: 0xaa0003f3  mov x19, x0
 2928: 0xf940027a  ldr x26, [x19]
 2932: 0xf940067b  ldr x27, [x19,#8]
 2936: 0xd2ffffd8  mov x24, #0xfffe000000000000
 2940: 0xd2ffffb9  mov x25, #0xfffd000000000000
 2944: 0x17fffd77  b .+0xfffffffffffff5dc
 2948: 0xd10203ff  sub sp, sp, #0x80
 2952: 0xa9007bfd  stp x29, x30, [sp]
 2956: 0x910003fd  mov x29, sp
 2960: 0xa90153f3  stp x19, x20, [sp,#16]
 2964: 0xa9025bf5  stp x21, x22, [sp,#32]
 2968: 0xa90363f7  stp x23, x24, [sp,#48]
 2972: 0xa9046bf9  stp x25, x26, [sp,#64]
 2976: 0xa90573fb  stp x27, x28, [sp,#80]
 2980: 0x6d0627e8  stp d8, d9, [sp,#96]
 2984: 0x6d072fea  stp d10, d11, [sp,#112]
 2988: 0xaa0003f3  mov x19, x0
 2992: 0xf940027a  ldr x26, [x19]
 2996: 0xf940067b  ldr x27, [x19,#8]
 3000: 0xd2ffffd8  mov x24, #0xfffe000000000000
 3004: 0xd2ffffb9  mov x25, #0xfffd000000000000
 3008: 0x17fffda9  b .+0xfffffffffffff6a4
 3012: 0xd10203ff  sub sp, sp, #0x80
 3016: 0xa9007bfd  stp x29, x30, [sp]
 3020: 0x910003fd  mov x29, sp
 3024: 0xa90153f3  stp x19, x20, [sp,#16]
 3028: 0xa9025bf5  stp x21, x22, [sp,#32]
 3032: 0xa90363f7  stp x23, x24, [sp,#48]
 3036: 0xa9046bf9  stp x25, x26, [sp,#64]
 3040: 0xa90573fb  stp x27, x28, [sp,#80]
 3044: 0x6d0627e8  stp d8, d9, [sp,#96]
 3048: 0x6d072fea  stp d10, d11, [sp,#112]
 3052: 0xaa0003f3  mov x19, x0
 3056: 0xf940027a  ldr x26, [x19]
 3060: 0xf940067b  ldr x27, [x19,#8]
 3064: 0xd2ffffd8  mov x24, #0xfffe000000000000
 3068: 0xd2ffffb9  mov x25, #0xfffd000000000000
 3072: 0x17fffe10  b .+0xfffffffffffff840
 3076: 0xd10203ff  sub sp, sp, #0x80
 3080: 0xa9007bfd  stp x29, x30, [sp]
 3084: 0x910003fd  mov x29, sp
 3088: 0xa90153f3  stp x19, x20, [sp,#16]
 3092: 0xa9025bf5  stp x21, x22, [sp,#32]
 3096: 0xa90363f7  stp x23, x24, [sp,#48]
 3100: 0xa9046bf9  stp x25, x26, [sp,#64]
 3104: 0xa90573fb  stp x27, x28, [sp,#80]
 3108: 0x6d0627e8  stp d8, d9, [sp,#96]
 3112: 0x6d072fea  stp d10, d11, [sp,#112]
 3116: 0xaa0003f3  mov x19, x0
 3120: 0xf940027a  ldr x26, [x19]
 3124: 0xf940067b  ldr x27, [x19,#8]
 3128: 0xd2ffffd8  mov x24, #0xfffe000000000000
 3132: 0xd2ffffb9  mov x25, #0xfffd000000000000
 3136: 0x17fffe40  b .+0xfffffffffffff900
 3140: 0xd10203ff  sub sp, sp, #0x80
 3144: 0xa9007bfd  stp x29, x30, [sp]
 3148: 0x910003fd  mov x29, sp
 3152: 0xa90153f3  stp x19, x20, [sp,#16]
 3156: 0xa9025bf5  stp x21, x22, [sp,#32]
 3160: 0xa90363f7  stp x23, x24, [sp,#48]
 3164: 0xa9046bf9  stp x25, x26, [sp,#64]
 3168: 0xa90573fb  stp x27, x28, [sp,#80]
 3172: 0x6d0627e8  stp d8, d9, [sp,#96]
 3176: 0x6d072fea  stp d10, d11, [sp,#112]
 3180: 0xaa0003f3  mov x19, x0
 3184: 0xf940027a  ldr x26, [x19]
 3188: 0xf940067b  ldr x27, [x19,#8]
 3192: 0xd2ffffd8  mov x24, #0xfffe000000000000
 3196: 0xd2ffffb9  mov x25, #0xfffd000000000000
 3200: 0x17fffe72  b .+0xfffffffffffff9c8
 3204: 0xd10203ff  sub sp, sp, #0x80
 3208: 0xa9007bfd  stp x29, x30, [sp]
 3212: 0x910003fd  mov x29, sp
 3216: 0xa90153f3  stp x19, x20, [sp,#16]
 3220: 0xa9025bf5  stp x21, x22, [sp,#32]
 3224: 0xa90363f7  stp x23, x24, [sp,#48]
 3228: 0xa9046bf9  stp x25, x26, [sp,#64]
 3232: 0xa90573fb  stp x27, x28, [sp,#80]
 3236: 0x6d0627e8  stp d8, d9, [sp,#96]
 3240: 0x6d072fea  stp d10, d11, [sp,#112]
 3244: 0xaa0003f3  mov x19, x0
 3248: 0xf940027a  ldr x26, [x19]
 3252: 0xf940067b  ldr x27, [x19,#8]
 3256: 0xd2ffffd8  mov x24, #0xfffe000000000000
 3260: 0xd2ffffb9  mov x25, #0xfffd000000000000
 3264: 0x17fffeff  b .+0xfffffffffffffbfc
 3268: 0xd10203ff  sub sp, sp, #0x80
 3272: 0xa9007bfd  stp x29, x30, [sp]
 3276: 0x910003fd  mov x29, sp
 3280: 0xa90153f3  stp x19, x20, [sp,#16]
 3284: 0xa9025bf5  stp x21, x22, [sp,#32]
 3288: 0xa90363f7  stp x23, x24, [sp,#48]
 3292: 0xa9046bf9  stp x25, x26, [sp,#64]
 3296: 0xa90573fb  stp x27, x28, [sp,#80]
 3300: 0x6d0627e8  stp d8, d9, [sp,#96]
 3304: 0x6d072fea  stp d10, d11, [sp,#112]
 3308: 0xaa0003f3  mov x19, x0
 3312: 0xf940027a  ldr x26, [x19]
 3316: 0xf940067b  ldr x27, [x19,#8]
 3320: 0xd2ffffd8  mov x24, #0xfffe000000000000
 3324: 0xd2ffffb9  mov x25, #0xfffd000000000000
 3328: 0x17ffff43  b .+0xfffffffffffffd0c
