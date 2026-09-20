package com.snowball.silverwing.desktop

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.snowball.silverwing.core.ThemePreference

val BrandBlue = Color(0xFF356AE6)
val BrandBlueDark = Color(0xFF2454C6)
val SuccessGreen = Color(0xFF16A34A)
val WarningAmber = Color(0xFFD97706)
val DangerRed = Color(0xFFDC2626)

private val LightColors = lightColorScheme(
    primary = BrandBlue,
    onPrimary = Color.White,
    primaryContainer = Color(0xFFE8EEFF),
    onPrimaryContainer = Color(0xFF173B86),
    secondary = Color(0xFF526178),
    secondaryContainer = Color(0xFFE8EDF5),
    tertiary = Color(0xFF6C55B5),
    background = Color(0xFFF4F7FB),
    surface = Color(0xFFFCFDFF),
    surfaceVariant = Color(0xFFF0F3F8),
    outline = Color(0xFFD3DAE6),
    outlineVariant = Color(0xFFE5EAF1),
    error = DangerRed,
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFF60A5FA),
    onPrimary = Color(0xFF082F6B),
    primaryContainer = Color(0xFF1E3A8A),
    onPrimaryContainer = Color(0xFFDBEAFE),
    secondary = Color(0xFF94A3B8),
    secondaryContainer = Color(0xFF273449),
    tertiary = Color(0xFFC5B4FF),
    background = Color(0xFF0C111B),
    surface = Color(0xFF121925),
    surfaceVariant = Color(0xFF1A2433),
    outline = Color(0xFF3A4658),
    outlineVariant = Color(0xFF263244),
    error = Color(0xFFF87171),
)

private val SilverWingTypography = Typography(
    headlineSmall = TextStyle(fontSize = 24.sp, lineHeight = 31.sp, fontWeight = FontWeight.Bold),
    titleLarge = TextStyle(fontSize = 20.sp, lineHeight = 27.sp, fontWeight = FontWeight.SemiBold),
    titleMedium = TextStyle(fontSize = 16.sp, lineHeight = 23.sp, fontWeight = FontWeight.SemiBold),
    titleSmall = TextStyle(fontSize = 14.sp, lineHeight = 20.sp, fontWeight = FontWeight.SemiBold),
    bodyLarge = TextStyle(fontSize = 15.sp, lineHeight = 23.sp),
    bodyMedium = TextStyle(fontSize = 14.sp, lineHeight = 21.sp),
    bodySmall = TextStyle(fontSize = 12.sp, lineHeight = 18.sp),
    labelLarge = TextStyle(fontSize = 14.sp, lineHeight = 20.sp, fontWeight = FontWeight.Medium),
    labelMedium = TextStyle(fontSize = 12.sp, lineHeight = 17.sp, fontWeight = FontWeight.Medium),
    labelSmall = TextStyle(fontSize = 11.sp, lineHeight = 16.sp, fontWeight = FontWeight.Medium),
)

private val SilverWingShapes = Shapes(
    extraSmall = RoundedCornerShape(7.dp),
    small = RoundedCornerShape(10.dp),
    medium = RoundedCornerShape(14.dp),
    large = RoundedCornerShape(18.dp),
    extraLarge = RoundedCornerShape(24.dp),
)

@Composable
fun SilverWingTheme(
    preference: ThemePreference,
    content: @Composable () -> Unit,
) {
    val dark = when (preference) {
        ThemePreference.SYSTEM -> isSystemInDarkTheme()
        ThemePreference.LIGHT -> false
        ThemePreference.DARK -> true
    }
    MaterialTheme(
        colorScheme = if (dark) DarkColors else LightColors,
        typography = SilverWingTypography,
        shapes = SilverWingShapes,
        content = content,
    )
}

fun ColorScheme.statusColor(status: String): Color = when {
    status.contains("READY") || status == "SUCCESS" || status in setOf("构建成功", "已构建", "UAT已发布", "已生产发布") -> SuccessGreen
    status == "UAT未发布" || status == "构建状态未知" -> outline
    status == "FAILED" || status == "CONFLICT" || status in setOf(
        "已创建",
        "预检通过",
        "源分支已推送",
        "构建失败",
        "UAT发布失败",
        "生产发布失败",
        "存在冲突",
    ) -> error
    status == "PARTIAL" || status == "部分完成" || status.contains("WARNING") -> WarningAmber
    else -> primary
}
