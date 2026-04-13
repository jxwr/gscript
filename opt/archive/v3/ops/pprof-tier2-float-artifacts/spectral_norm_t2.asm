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
   64: 0xaa1403e0  mov x0, x20
   68: 0xd370fc02  lsr x2, x0, #48
   72: 0xd29fffc3  mov x3, #0xfffe
   76: 0xeb03005f  cmp x2, x3
   80: 0x54000081  b.ne .+0x10
   84: 0xaa0003f5  mov x21, x0
   88: 0xf9004f55  str x21, [x26,#152]
   92: 0x14000004  b .+0x10
   96: 0xd2800040  mov x0, #0x2
  100: 0xf9000a60  str x0, [x19,#16]
  104: 0x1400019f  b .+0x67c
  108: 0xf9400b56  ldr x22, [x26,#16]
  112: 0xd2800037  mov x23, #0x1
  116: 0x9340bea0  sbfx x0, x21, #0, #48
  120: 0xd100041c  sub x28, x0, #0x1
  124: 0x9340bf80  sbfx x0, x28, #0, #48
  128: 0xeb1c001f  cmp x0, x28
  132: 0x54000140  b.eq .+0x28
  136: 0xd340bee0  ubfx x0, x23, #0, #48
  140: 0xaa180000  orr x0, x0, x24
  144: 0xf9005340  str x0, [x26,#160]
  148: 0xf9000354  str x20, [x26]
  152: 0xf9004f55  str x21, [x26,#152]
  156: 0xf9000b56  str x22, [x26,#16]
  160: 0xd2800040  mov x0, #0x2
  164: 0xf9000a60  str x0, [x19,#16]
  168: 0x1400018f  b .+0x63c
  172: 0xd340bf80  ubfx x0, x28, #0, #48
  176: 0xaa180000  orr x0, x0, x24
  180: 0xf9005740  str x0, [x26,#168]
  184: 0xd2800037  mov x23, #0x1
  188: 0xd340bee0  ubfx x0, x23, #0, #48
  192: 0xaa180000  orr x0, x0, x24
  196: 0xf9005b40  str x0, [x26,#176]
  200: 0x92800014  mov x20, #0xffffffffffffffff
  204: 0xd340be80  ubfx x0, x20, #0, #48
  208: 0xaa180000  orr x0, x0, x24
  212: 0xf9005f40  str x0, [x26,#184]
  216: 0x1400016e  b .+0x5b8
  220: 0xd2800000  mov x0, #0x0
  224: 0x9e670004  fmov d4, x0
  228: 0x9e660080  fmov x0, d4
  232: 0xf9006340  str x0, [x26,#192]
  236: 0xd2800034  mov x20, #0x1
  240: 0xf9404f40  ldr x0, [x26,#152]
  244: 0x9340bc00  sbfx x0, x0, #0, #48
  248: 0xd1000415  sub x21, x0, #0x1
  252: 0x9340bea0  sbfx x0, x21, #0, #48
  256: 0xeb15001f  cmp x0, x21
  260: 0x540000e0  b.eq .+0x1c
  264: 0xd340be80  ubfx x0, x20, #0, #48
  268: 0xaa180000  orr x0, x0, x24
  272: 0xf9006740  str x0, [x26,#200]
  276: 0xd2800040  mov x0, #0x2
  280: 0xf9000a60  str x0, [x19,#16]
  284: 0x14000172  b .+0x5c8
  288: 0xd340bea0  ubfx x0, x21, #0, #48
  292: 0xaa180000  orr x0, x0, x24
  296: 0xf9006b40  str x0, [x26,#208]
  300: 0xd2800034  mov x20, #0x1
  304: 0xd340be80  ubfx x0, x20, #0, #48
  308: 0xaa180000  orr x0, x0, x24
  312: 0xf9006f40  str x0, [x26,#216]
  316: 0x92800016  mov x22, #0xffffffffffffffff
  320: 0x9e660080  fmov x0, d4
  324: 0xf9008b40  str x0, [x26,#272]
  328: 0xaa1603f4  mov x20, x22
  332: 0x140000fd  b .+0x3f4
  336: 0x92800000  mov x0, #0xffffffffffffffff
  340: 0xf900d260  str x0, [x19,#416]
  344: 0xd28003a0  mov x0, #0x1d
  348: 0xf9002260  str x0, [x19,#64]
  352: 0xd2800020  mov x0, #0x1
  356: 0xf9002660  str x0, [x19,#72]
  360: 0xd2800220  mov x0, #0x11
  364: 0xf9002a60  str x0, [x19,#80]
  368: 0xd2800080  mov x0, #0x4
  372: 0xf9000a60  str x0, [x19,#16]
  376: 0x1400015b  b .+0x56c
  380: 0xf9407740  ldr x0, [x26,#232]
  384: 0xaa0003f4  mov x20, x0
  388: 0xaa1403e0  mov x0, x20
  392: 0xf9003b40  str x0, [x26,#112]
  396: 0xf940a340  ldr x0, [x26,#320]
  400: 0xf9003f40  str x0, [x26,#120]
  404: 0xf9409340  ldr x0, [x26,#288]
  408: 0xf9004340  str x0, [x26,#128]
  412: 0x9e660080  fmov x0, d4
  416: 0xf9008b40  str x0, [x26,#272]
  420: 0xf940be63  ldr x3, [x19,#376]
  424: 0xf100c07f  cmp x3, #0x30
  428: 0x5400098a  b.ge .+0x130
  432: 0xf9403b40  ldr x0, [x26,#112]
  436: 0xd370fc01  lsr x1, x0, #48
  440: 0xd29fffe2  mov x2, #0xffff
  444: 0xeb02003f  cmp x1, x2
  448: 0x540008e1  b.ne .+0x11c
  452: 0xd36cfc01  lsr x1, x0, #44
  456: 0xd28001e2  mov x2, #0xf
  460: 0x8a020021  and x1, x1, x2
  464: 0xf100203f  cmp x1, #0x8
  468: 0x54000841  b.ne .+0x108
  472: 0xd340ac00  ubfx x0, x0, #0, #44
  476: 0xf9400001  ldr x1, [x0]
  480: 0xf9408c22  ldr x2, [x1,#280]
  484: 0xb40007c2  cbz x2, .+0xf8
  488: 0xf9401c23  ldr x3, [x1,#56]
  492: 0xd37df063  lsl x3, x3, #3
  496: 0x91054063  add x3, x3, #0x150
  500: 0x8b1a0063  add x3, x3, x26
  504: 0xf940b264  ldr x4, [x19,#352]
  508: 0xeb04007f  cmp x3, x4
  512: 0x540006e8  b.hi .+0xdc
  516: 0xf9407823  ldr x3, [x1,#240]
  520: 0x91000463  add x3, x3, #0x1
  524: 0xf9007823  str x3, [x1,#240]
  528: 0xf100087f  cmp x3, #0x2
  532: 0x54000640  b.eq .+0xc8
  536: 0xd10103ff  sub sp, sp, #0x40
  540: 0xa9007bfd  stp x29, x30, [sp]
  544: 0xa9016ffa  stp x26, x27, [sp,#16]
  548: 0xf9409663  ldr x3, [x19,#296]
  552: 0xf90013e3  str x3, [sp,#32]
  556: 0xf9407a63  ldr x3, [x19,#240]
  560: 0xf90017e3  str x3, [sp,#40]
  564: 0xf9408263  ldr x3, [x19,#256]
  568: 0xf9001be3  str x3, [sp,#48]
  572: 0xf9403f43  ldr x3, [x26,#120]
  576: 0xf900ab43  str x3, [x26,#336]
  580: 0xf9404343  ldr x3, [x26,#128]
  584: 0xf900af43  str x3, [x26,#344]
  588: 0x9105435a  add x26, x26, #0x150
  592: 0xf900027a  str x26, [x19]
  596: 0xf9402c3b  ldr x27, [x1,#88]
  600: 0xf900067b  str x27, [x19,#8]
  604: 0xf9007a60  str x0, [x19,#240]
  608: 0xd2800023  mov x3, #0x1
  612: 0xf9009663  str x3, [x19,#296]
  616: 0xf9409023  ldr x3, [x1,#288]
  620: 0xf9008263  str x3, [x19,#256]
  624: 0xf940be63  ldr x3, [x19,#376]
  628: 0x91000463  add x3, x3, #0x1
  632: 0xf900be63  str x3, [x19,#376]
  636: 0xaa1303e0  mov x0, x19
  640: 0xd63f0040  blr x2
  644: 0xf940be63  ldr x3, [x19,#376]
  648: 0xd1000463  sub x3, x3, #0x1
  652: 0xf900be63  str x3, [x19,#376]
  656: 0xa9416ffa  ldp x26, x27, [sp,#16]
  660: 0xf94013e3  ldr x3, [sp,#32]
  664: 0xf9009663  str x3, [x19,#296]
  668: 0xf94017e3  ldr x3, [sp,#40]
  672: 0xf9007a63  str x3, [x19,#240]
  676: 0xf9401be3  ldr x3, [sp,#48]
  680: 0xf9008263  str x3, [x19,#256]
  684: 0xa9407bfd  ldp x29, x30, [sp]
  688: 0x910103ff  add sp, sp, #0x40
  692: 0xf900027a  str x26, [x19]
  696: 0xf900067b  str x27, [x19,#8]
  700: 0xf9400a63  ldr x3, [x19,#16]
  704: 0xb50000e3  cbnz x3, .+0x1c
  708: 0xf9407e60  ldr x0, [x19,#248]
  712: 0xf9003b40  str x0, [x26,#112]
  716: 0xfd408b44  ldr d4, [x26,#272]
  720: 0xf9403b40  ldr x0, [x26,#112]
  724: 0xaa0003f5  mov x21, x0
  728: 0x14000012  b .+0x48
  732: 0xf9007754  str x20, [x26,#232]
  736: 0xf9007b55  str x21, [x26,#240]
  740: 0xd28001c0  mov x0, #0xe
  744: 0xf9001260  str x0, [x19,#32]
  748: 0xd2800040  mov x0, #0x2
  752: 0xf9001660  str x0, [x19,#40]
  756: 0xd2800020  mov x0, #0x1
  760: 0xf9001a60  str x0, [x19,#48]
  764: 0xd2800280  mov x0, #0x14
  768: 0xf9001e60  str x0, [x19,#56]
  772: 0xd2800060  mov x0, #0x3
  776: 0xf9000a60  str x0, [x19,#16]
  780: 0x140000f6  b .+0x3d8
  784: 0xf9407754  ldr x20, [x26,#232]
  788: 0xf9407b55  ldr x21, [x26,#240]
  792: 0xf9403b40  ldr x0, [x26,#112]
  796: 0xaa0003f5  mov x21, x0
  800: 0xf9400740  ldr x0, [x26,#8]
  804: 0xd370fc01  lsr x1, x0, #48
  808: 0xd29fffe2  mov x2, #0xffff
  812: 0xeb02003f  cmp x1, x2
  816: 0x540004c1  b.ne .+0x98
  820: 0xd36cfc01  lsr x1, x0, #44
  824: 0xd28001e2  mov x2, #0xf
  828: 0x8a020021  and x1, x1, x2
  832: 0xf100003f  cmp x1, #0x0
  836: 0x54000421  b.ne .+0x84
  840: 0xd340ac00  ubfx x0, x0, #0, #44
  844: 0xb40003e0  cbz x0, .+0x7c
  848: 0xf9403401  ldr x1, [x0,#104]
  852: 0xb50003a1  cbnz x1, .+0x74
  856: 0xf9409341  ldr x1, [x26,#288]
  860: 0xd370fc22  lsr x2, x1, #48
  864: 0xd29fffc3  mov x3, #0xfffe
  868: 0xeb03005f  cmp x2, x3
  872: 0x54000301  b.ne .+0x60
  876: 0x9340bc21  sbfx x1, x1, #0, #48
  880: 0xf100003f  cmp x1, #0x0
  884: 0x540002ab  b.lt .+0x54
  888: 0x39422402  ldrb w2, [x0,#137]
  892: 0xf100045f  cmp x2, #0x1
  896: 0x54000120  b.eq .+0x24
  900: 0xb5000222  cbnz x2, .+0x44
  904: 0xf9400802  ldr x2, [x0,#16]
  908: 0xeb02003f  cmp x1, x2
  912: 0x540001ca  b.ge .+0x38
  916: 0xf9400402  ldr x2, [x0,#8]
  920: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  924: 0xaa0003f4  mov x20, x0
  928: 0x14000021  b .+0x84
  932: 0xf9404c02  ldr x2, [x0,#152]
  936: 0xeb02003f  cmp x1, x2
  940: 0x540000ea  b.ge .+0x1c
  944: 0xf9404802  ldr x2, [x0,#144]
  948: 0xf8617840  ldr x0, [x2,x1,lsl #3]
  952: 0xd340bc00  ubfx x0, x0, #0, #48
  956: 0xaa180000  orr x0, x0, x24
  960: 0xaa0003f4  mov x20, x0
  964: 0x14000018  b .+0x60
  968: 0xf9400740  ldr x0, [x26,#8]
  972: 0xf9000740  str x0, [x26,#8]
  976: 0xf9409340  ldr x0, [x26,#288]
  980: 0xf9009340  str x0, [x26,#288]
  984: 0xf9007f54  str x20, [x26,#248]
  988: 0xf9007b55  str x21, [x26,#240]
  992: 0xd2800020  mov x0, #0x1
  996: 0xf9002e60  str x0, [x19,#88]
 1000: 0xd2800020  mov x0, #0x1
 1004: 0xf9003260  str x0, [x19,#96]
 1008: 0xd2800480  mov x0, #0x24
 1012: 0xf9003660  str x0, [x19,#104]
 1016: 0xd28003e0  mov x0, #0x1f
 1020: 0xf9003e60  str x0, [x19,#120]
 1024: 0xd28002c0  mov x0, #0x16
 1028: 0xf9004660  str x0, [x19,#136]
 1032: 0xd28000a0  mov x0, #0x5
 1036: 0xf9000a60  str x0, [x19,#16]
 1040: 0x140000b5  b .+0x2d4
 1044: 0xf9407f54  ldr x20, [x26,#248]
 1048: 0xf9407b55  ldr x21, [x26,#240]
 1052: 0xf9407f40  ldr x0, [x26,#248]
 1056: 0xaa0003f4  mov x20, x0
 1060: 0xaa1503e0  mov x0, x21
 1064: 0xaa1403e1  mov x1, x20
 1068: 0xd370fc02  lsr x2, x0, #48
 1072: 0xd29fffc3  mov x3, #0xfffe
 1076: 0xeb03005f  cmp x2, x3
 1080: 0x54000161  b.ne .+0x2c
 1084: 0xd370fc22  lsr x2, x1, #48
 1088: 0xd29fffc3  mov x3, #0xfffe
 1092: 0xeb03005f  cmp x2, x3
 1096: 0x540001e1  b.ne .+0x3c
 1100: 0x9340bc00  sbfx x0, x0, #0, #48
 1104: 0x9340bc21  sbfx x1, x1, #0, #48
 1108: 0x9b017c00  mul x0, x0, x1
 1112: 0xd340bc00  ubfx x0, x0, #0, #48
 1116: 0xaa180000  orr x0, x0, x24
 1120: 0x14000010  b .+0x40
 1124: 0x9e670000  fmov d0, x0
 1128: 0xd370fc22  lsr x2, x1, #48
 1132: 0xd29fffc3  mov x3, #0xfffe
 1136: 0xeb03005f  cmp x2, x3
 1140: 0x54000101  b.ne .+0x20
 1144: 0x9340bc21  sbfx x1, x1, #0, #48
 1148: 0x9e620021  scvtf d1, x1
 1152: 0x14000006  b .+0x18
 1156: 0x9340bc00  sbfx x0, x0, #0, #48
 1160: 0x9e620000  scvtf d0, x0
 1164: 0x9e670021  fmov d1, x1
 1168: 0x14000002  b .+0x8
 1172: 0x9e670021  fmov d1, x1
 1176: 0x1e610800  fmul d0, d0, d1
 1180: 0x9e660000  fmov x0, d0
 1184: 0xaa0003f6  mov x22, x0
 1188: 0x9e660080  fmov x0, d4
 1192: 0xaa1603e1  mov x1, x22
 1196: 0xd370fc02  lsr x2, x0, #48
 1200: 0xd29fffc3  mov x3, #0xfffe
 1204: 0xeb03005f  cmp x2, x3
 1208: 0x54000161  b.ne .+0x2c
 1212: 0xd370fc22  lsr x2, x1, #48
 1216: 0xd29fffc3  mov x3, #0xfffe
 1220: 0xeb03005f  cmp x2, x3
 1224: 0x540001e1  b.ne .+0x3c
 1228: 0x9340bc00  sbfx x0, x0, #0, #48
 1232: 0x9340bc21  sbfx x1, x1, #0, #48
 1236: 0x8b010000  add x0, x0, x1
 1240: 0xd340bc00  ubfx x0, x0, #0, #48
 1244: 0xaa180000  orr x0, x0, x24
 1248: 0x14000010  b .+0x40
 1252: 0x9e670000  fmov d0, x0
 1256: 0xd370fc22  lsr x2, x1, #48
 1260: 0xd29fffc3  mov x3, #0xfffe
 1264: 0xeb03005f  cmp x2, x3
 1268: 0x54000101  b.ne .+0x20
 1272: 0x9340bc21  sbfx x1, x1, #0, #48
 1276: 0x9e620021  scvtf d1, x1
 1280: 0x14000006  b .+0x18
 1284: 0x9340bc00  sbfx x0, x0, #0, #48
 1288: 0x9e620000  scvtf d0, x0
 1292: 0x9e670021  fmov d1, x1
 1296: 0x14000002  b .+0x8
 1300: 0x9e670021  fmov d1, x1
 1304: 0x1e612800  fadd d0, d0, d1
 1308: 0x9e660000  fmov x0, d0
 1312: 0xaa0003f4  mov x20, x0
 1316: 0xf9008754  str x20, [x26,#264]
 1320: 0x9e670284  fmov d4, x20
 1324: 0x9e660080  fmov x0, d4
 1328: 0xf9008b40  str x0, [x26,#272]
 1332: 0xf9409340  ldr x0, [x26,#288]
 1336: 0x9340bc14  sbfx x20, x0, #0, #48
 1340: 0x14000001  b .+0x4
 1344: 0x91000695  add x21, x20, #0x1
 1348: 0xd340bea0  ubfx x0, x21, #0, #48
 1352: 0xaa180000  orr x0, x0, x24
 1356: 0xf9009340  str x0, [x26,#288]
 1360: 0xf9406b41  ldr x1, [x26,#208]
 1364: 0x9340bc21  sbfx x1, x1, #0, #48
 1368: 0xeb0102bf  cmp x21, x1
 1372: 0x9a9fc7e0  cset x0, le
 1376: 0xaa190000  orr x0, x0, x25
 1380: 0xaa0003f4  mov x20, x0
 1384: 0x37000054  tbnz w20, #0, .+0x8
 1388: 0x14000002  b .+0x8
 1392: 0x17fffef8  b .+0xfffffffffffffbe0
 1396: 0xf9400b40  ldr x0, [x26,#16]
 1400: 0xd370fc01  lsr x1, x0, #48
 1404: 0xd29fffe2  mov x2, #0xffff
 1408: 0xeb02003f  cmp x1, x2
 1412: 0x540005a1  b.ne .+0xb4
 1416: 0xd36cfc01  lsr x1, x0, #44
 1420: 0xd28001e2  mov x2, #0xf
 1424: 0x8a020021  and x1, x1, x2
 1428: 0xf100003f  cmp x1, #0x0
 1432: 0x54000501  b.ne .+0xa0
 1436: 0xd340ac00  ubfx x0, x0, #0, #44
 1440: 0xb40004c0  cbz x0, .+0x98
 1444: 0xf9403401  ldr x1, [x0,#104]
 1448: 0xb5000481  cbnz x1, .+0x90
 1452: 0xf940a341  ldr x1, [x26,#320]
 1456: 0xd370fc22  lsr x2, x1, #48
 1460: 0xd29fffc3  mov x3, #0xfffe
 1464: 0xeb03005f  cmp x2, x3
 1468: 0x540003e1  b.ne .+0x7c
 1472: 0x9340bc21  sbfx x1, x1, #0, #48
 1476: 0xf100003f  cmp x1, #0x0
 1480: 0x5400038b  b.lt .+0x70
 1484: 0x39422402  ldrb w2, [x0,#137]
 1488: 0xf100045f  cmp x2, #0x1
 1492: 0x54000160  b.eq .+0x2c
 1496: 0xb5000302  cbnz x2, .+0x60
 1500: 0xf9400802  ldr x2, [x0,#16]
 1504: 0xeb02003f  cmp x1, x2
 1508: 0x540002aa  b.ge .+0x54
 1512: 0xf9408b44  ldr x4, [x26,#272]
 1516: 0xf9400402  ldr x2, [x0,#8]
 1520: 0xf8217844  str x4, [x2,x1,lsl #3]
 1524: 0xd2800025  mov x5, #0x1
 1528: 0x39022005  strb w5, [x0,#136]
 1532: 0x14000022  b .+0x88
 1536: 0xf9404c02  ldr x2, [x0,#152]
 1540: 0xeb02003f  cmp x1, x2
 1544: 0x5400018a  b.ge .+0x30
 1548: 0xf9408b44  ldr x4, [x26,#272]
 1552: 0xd370fc85  lsr x5, x4, #48
 1556: 0xd29fffc6  mov x6, #0xfffe
 1560: 0xeb0600bf  cmp x5, x6
 1564: 0x540000e1  b.ne .+0x1c
 1568: 0x9340bc84  sbfx x4, x4, #0, #48
 1572: 0xf9404802  ldr x2, [x0,#144]
 1576: 0xf8217844  str x4, [x2,x1,lsl #3]
 1580: 0xd2800025  mov x5, #0x1
 1584: 0x39022005  strb w5, [x0,#136]
 1588: 0x14000014  b .+0x50
 1592: 0xf9400b40  ldr x0, [x26,#16]
 1596: 0xf9000b40  str x0, [x26,#16]
 1600: 0xf940a340  ldr x0, [x26,#320]
 1604: 0xf900a340  str x0, [x26,#320]
 1608: 0xf9408b40  ldr x0, [x26,#272]
 1612: 0xf9008b40  str x0, [x26,#272]
 1616: 0xd2800040  mov x0, #0x2
 1620: 0xf9002e60  str x0, [x19,#88]
 1624: 0xd2800040  mov x0, #0x2
 1628: 0xf9003260  str x0, [x19,#96]
 1632: 0xd2800500  mov x0, #0x28
 1636: 0xf9003660  str x0, [x19,#104]
 1640: 0xd2800440  mov x0, #0x22
 1644: 0xf9003a60  str x0, [x19,#112]
 1648: 0xd28004e0  mov x0, #0x27
 1652: 0xf9004660  str x0, [x19,#136]
 1656: 0xd28000a0  mov x0, #0x5
 1660: 0xf9000a60  str x0, [x19,#16]
 1664: 0x14000019  b .+0x64
 1668: 0xf940a340  ldr x0, [x26,#320]
 1672: 0x9340bc14  sbfx x20, x0, #0, #48
 1676: 0x14000001  b .+0x4
 1680: 0x91000695  add x21, x20, #0x1
 1684: 0xd340bea0  ubfx x0, x21, #0, #48
 1688: 0xaa180000  orr x0, x0, x24
 1692: 0xf900a340  str x0, [x26,#320]
 1696: 0xf9405741  ldr x1, [x26,#168]
 1700: 0x9340bc21  sbfx x1, x1, #0, #48
 1704: 0xeb0102bf  cmp x21, x1
 1708: 0x9a9fc7e0  cset x0, le
 1712: 0xaa190000  orr x0, x0, x25
 1716: 0xaa0003f4  mov x20, x0
 1720: 0x37000054  tbnz w20, #0, .+0x8
 1724: 0x14000002  b .+0x8
 1728: 0x17fffe87  b .+0xfffffffffffffa1c
 1732: 0xd2ffff80  mov x0, #0xfffc000000000000
 1736: 0xf9000340  str x0, [x26]
 1740: 0xf9007e60  str x0, [x19,#248]
 1744: 0xf9409661  ldr x1, [x19,#296]
 1748: 0xb50003c1  cbnz x1, .+0x78
 1752: 0x14000001  b .+0x4
 1756: 0xd2800000  mov x0, #0x0
 1760: 0xf9000a60  str x0, [x19,#16]
 1764: 0x6d4627e8  ldp d8, d9, [sp,#96]
 1768: 0x6d472fea  ldp d10, d11, [sp,#112]
 1772: 0xa94573fb  ldp x27, x28, [sp,#80]
 1776: 0xa9446bf9  ldp x25, x26, [sp,#64]
 1780: 0xa94363f7  ldp x23, x24, [sp,#48]
 1784: 0xa9425bf5  ldp x21, x22, [sp,#32]
 1788: 0xa94153f3  ldp x19, x20, [sp,#16]
 1792: 0xa9407bfd  ldp x29, x30, [sp]
 1796: 0x910203ff  add sp, sp, #0x80
 1800: 0xd65f03c0  ret
 1804: 0xd10203ff  sub sp, sp, #0x80
 1808: 0xa9007bfd  stp x29, x30, [sp]
 1812: 0x910003fd  mov x29, sp
 1816: 0xa90153f3  stp x19, x20, [sp,#16]
 1820: 0xa9025bf5  stp x21, x22, [sp,#32]
 1824: 0xa90363f7  stp x23, x24, [sp,#48]
 1828: 0xa9046bf9  stp x25, x26, [sp,#64]
 1832: 0xa90573fb  stp x27, x28, [sp,#80]
 1836: 0x6d0627e8  stp d8, d9, [sp,#96]
 1840: 0x6d072fea  stp d10, d11, [sp,#112]
 1844: 0xaa0003f3  mov x19, x0
 1848: 0xf940027a  ldr x26, [x19]
 1852: 0xf940067b  ldr x27, [x19,#8]
 1856: 0xd2ffffd8  mov x24, #0xfffe000000000000
 1860: 0xd2ffffb9  mov x25, #0xfffd000000000000
 1864: 0x17fffe3d  b .+0xfffffffffffff8f4
 1868: 0xd2800000  mov x0, #0x0
 1872: 0xf9000a60  str x0, [x19,#16]
 1876: 0x6d4627e8  ldp d8, d9, [sp,#96]
 1880: 0x6d472fea  ldp d10, d11, [sp,#112]
 1884: 0xa94573fb  ldp x27, x28, [sp,#80]
 1888: 0xa9446bf9  ldp x25, x26, [sp,#64]
 1892: 0xa94363f7  ldp x23, x24, [sp,#48]
 1896: 0xa9425bf5  ldp x21, x22, [sp,#32]
 1900: 0xa94153f3  ldp x19, x20, [sp,#16]
 1904: 0xa9407bfd  ldp x29, x30, [sp]
 1908: 0x910203ff  add sp, sp, #0x80
 1912: 0xd65f03c0  ret
 1916: 0xd10203ff  sub sp, sp, #0x80
 1920: 0xa9007bfd  stp x29, x30, [sp]
 1924: 0x910003fd  mov x29, sp
 1928: 0xa90153f3  stp x19, x20, [sp,#16]
 1932: 0xa9025bf5  stp x21, x22, [sp,#32]
 1936: 0xa90363f7  stp x23, x24, [sp,#48]
 1940: 0xa9046bf9  stp x25, x26, [sp,#64]
 1944: 0xa90573fb  stp x27, x28, [sp,#80]
 1948: 0x6d0627e8  stp d8, d9, [sp,#96]
 1952: 0x6d072fea  stp d10, d11, [sp,#112]
 1956: 0xaa0003f3  mov x19, x0
 1960: 0xf940027a  ldr x26, [x19]
 1964: 0xf940067b  ldr x27, [x19,#8]
 1968: 0xd2ffffd8  mov x24, #0xfffe000000000000
 1972: 0xd2ffffb9  mov x25, #0xfffd000000000000
 1976: 0x17fffe71  b .+0xfffffffffffff9c4
 1980: 0xd10203ff  sub sp, sp, #0x80
 1984: 0xa9007bfd  stp x29, x30, [sp]
 1988: 0x910003fd  mov x29, sp
 1992: 0xa90153f3  stp x19, x20, [sp,#16]
 1996: 0xa9025bf5  stp x21, x22, [sp,#32]
 2000: 0xa90363f7  stp x23, x24, [sp,#48]
 2004: 0xa9046bf9  stp x25, x26, [sp,#64]
 2008: 0xa90573fb  stp x27, x28, [sp,#80]
 2012: 0x6d0627e8  stp d8, d9, [sp,#96]
 2016: 0x6d072fea  stp d10, d11, [sp,#112]
 2020: 0xaa0003f3  mov x19, x0
 2024: 0xf940027a  ldr x26, [x19]
 2028: 0xf940067b  ldr x27, [x19,#8]
 2032: 0xd2ffffd8  mov x24, #0xfffe000000000000
 2036: 0xd2ffffb9  mov x25, #0xfffd000000000000
 2040: 0x17fffec6  b .+0xfffffffffffffb18
 2044: 0xd10203ff  sub sp, sp, #0x80
 2048: 0xa9007bfd  stp x29, x30, [sp]
 2052: 0x910003fd  mov x29, sp
 2056: 0xa90153f3  stp x19, x20, [sp,#16]
 2060: 0xa9025bf5  stp x21, x22, [sp,#32]
 2064: 0xa90363f7  stp x23, x24, [sp,#48]
 2068: 0xa9046bf9  stp x25, x26, [sp,#64]
 2072: 0xa90573fb  stp x27, x28, [sp,#80]
 2076: 0x6d0627e8  stp d8, d9, [sp,#96]
 2080: 0x6d072fea  stp d10, d11, [sp,#112]
 2084: 0xaa0003f3  mov x19, x0
 2088: 0xf940027a  ldr x26, [x19]
 2092: 0xf940067b  ldr x27, [x19,#8]
 2096: 0xd2ffffd8  mov x24, #0xfffe000000000000
 2100: 0xd2ffffb9  mov x25, #0xfffd000000000000
 2104: 0x17fffef7  b .+0xfffffffffffffbdc
 2108: 0xd10203ff  sub sp, sp, #0x80
 2112: 0xa9007bfd  stp x29, x30, [sp]
 2116: 0x910003fd  mov x29, sp
 2120: 0xa90153f3  stp x19, x20, [sp,#16]
 2124: 0xa9025bf5  stp x21, x22, [sp,#32]
 2128: 0xa90363f7  stp x23, x24, [sp,#48]
 2132: 0xa9046bf9  stp x25, x26, [sp,#64]
 2136: 0xa90573fb  stp x27, x28, [sp,#80]
 2140: 0x6d0627e8  stp d8, d9, [sp,#96]
 2144: 0x6d072fea  stp d10, d11, [sp,#112]
 2148: 0xaa0003f3  mov x19, x0
 2152: 0xf940027a  ldr x26, [x19]
 2156: 0xf940067b  ldr x27, [x19,#8]
 2160: 0xd2ffffd8  mov x24, #0xfffe000000000000
 2164: 0xd2ffffb9  mov x25, #0xfffd000000000000
 2168: 0x17ffff83  b .+0xfffffffffffffe0c
