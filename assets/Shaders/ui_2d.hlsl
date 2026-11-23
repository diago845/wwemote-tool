
Texture1D<float4> IniParams : register(t120);

#define SIZE   IniParams[87].xy
#define OFFSET IniParams[87].zw
#define COLOR  IniParams[88]

struct vs2ps {
	float4 pos : SV_Position0;
};

#ifdef VERTEX_SHADER
void main(
		out vs2ps output,
		uint vertex : SV_VertexID)
{
	float2 BaseCoord, Offset;
	Offset.x = OFFSET.x * 2 - 1;
	Offset.y = (1 - OFFSET.y) * 2 - 1;
	BaseCoord.xy = float2((2 * SIZE.x), (2 * (-SIZE.y)));
	switch (vertex) {
		case 0:
			output.pos.xy = float2(BaseCoord.x + Offset.x, BaseCoord.y + Offset.y);
			break;
		case 1:
			output.pos.xy = float2(BaseCoord.x + Offset.x, 0 + Offset.y);
			break;
		case 2:
			output.pos.xy = float2(0 + Offset.x, BaseCoord.y + Offset.y);
			break;
		case 3:
			output.pos.xy = float2(0 + Offset.x, 0 + Offset.y);
			break;
		default:
			output.pos.xy = 0;
			break;
	};
	output.pos.zw = float2(0, 1);
}
#endif

#ifdef PIXEL_SHADER
void main(vs2ps input, out float4 result : SV_Target0)
{
	result = COLOR;
}
#endif
