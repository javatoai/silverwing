package com.snowball.silverwing.core

import kotlinx.serialization.KSerializer
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import java.math.BigDecimal

/** Preserve personal-estimate precision in the local read cache. */
internal object RequirementEstimateSerializer : KSerializer<BigDecimal> {
    override val descriptor = PrimitiveSerialDescriptor("RequirementEstimate", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: BigDecimal) = encoder.encodeString(value.toPlainString())
    override fun deserialize(decoder: Decoder): BigDecimal = decoder.decodeString().toBigDecimal()
}
